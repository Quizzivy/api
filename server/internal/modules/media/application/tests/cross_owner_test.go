//go:build integration

package application_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"quizzivy/internal/core/adapters"
	"quizzivy/internal/modules/media/application"
	"quizzivy/internal/modules/media/application/command"
	"quizzivy/internal/modules/media/application/query"
	"quizzivy/internal/modules/media/domain"
	"quizzivy/internal/modules/media/repositories"
	questionsapp "quizzivy/internal/modules/questions/application"
	questionscommand "quizzivy/internal/modules/questions/application/command"
	questions "quizzivy/internal/modules/questions/domain"
	questionsrepo "quizzivy/internal/modules/questions/repositories"
	"quizzivy/internal/platform/db"
	"quizzivy/internal/shared/access"
)

// sharedQuestion is a question of owner's binding asset, held by the draft of
// a test of owner's that is shared with grantee as Can use, so the grantee
// reads it (and its media) through the share.
func (w *replacementWorld) sharedQuestion(t *testing.T, owner, asset string, kind domain.Kind, grantee string) (question, test string) {
	t.Helper()
	question = w.question(t, owner, asset, kind, nil)
	test, section := w.section(t, owner)
	w.exec(t, `INSERT INTO app.test_section_questions (test_section_id, ordinal, question_id) VALUES ($1, 0, $2)`, section, question)
	w.exec(t, `INSERT INTO app.content_shares (kind, test_id, grantee_id, access, granted_by) VALUES ('test', $1, $2, 'use', $3)`, test, grantee, owner)
	return question, test
}

// copyAs copies the questions ids into copier's bank as the questions
// module's copy does (copyTest, copyQuestionSet, question-copies), reading
// them under copier's own scope, and commits.
func (w *replacementWorld) copyAs(ctx context.Context, copier string, ids ...string) ([]string, error) {
	tx, err := w.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	copies, err := questionsrepo.CopyQuestions(ctx, tx, adapters.QuestionMedia{}, questions.Copy{IDs: ids, Reader: access.Scope{UserID: copier},
		OwnerID: copier, ActorID: copier, AuditEach: true, Now: time.Now()})
	if err != nil {
		return nil, err
	}
	return copies, tx.Commit(ctx)
}

// versionUsing publishes a version of a new test of owner's whose question
// shows asset, and answers the test.
func (w *replacementWorld) versionUsing(t *testing.T, owner, asset string) string {
	t.Helper()
	test := w.id(t, `INSERT INTO app.tests (title, status, current_version, created_by, owner_id) VALUES ('Đề đã xuất bản', 'published', 1, $1, $1) RETURNING id::text`, owner)
	version := w.id(t, `INSERT INTO app.test_versions (test_id, version, total_points, published_by) VALUES ($1, 1, 1, $2) RETURNING id::text`, test, owner)
	section := w.id(t, `INSERT INTO app.test_version_sections (test_version_id, ordinal, title) VALUES ($1, 0, 'Phần') RETURNING id::text`, version)
	w.exec(t, `INSERT INTO app.test_version_questions (test_version_section_id, ordinal, type, prompt, points, media_asset_id, media_asset_kind)
		VALUES ($1, 0, 'short_answer', 'Hỏi', 1, $2, 'image')`, section, asset)
	return test
}

func (w *replacementWorld) media() *application.Application {
	return application.New(w.repo, newFakeStore(), audioProbe{})
}

func (w *replacementWorld) deleteAs(actor, asset string) error {
	_, err := w.media().Commands.Delete.Handle(context.Background(), command.Delete{Input: domain.DeleteInput{ID: asset, ActorID: actor}})
	return err
}

// TestDeletingAFileAnotherOwnersContentUsesIsRefusedWithACount: once B holds
// a copy of A's question, A's delete of its file answers
// MEDIA_IN_USE_ELSEWHERE counting B's copy, then B's group and B's published
// version too, and names none of them; A's own published version still
// refuses it as MEDIA_REFERENCED, naming A's test alone. A file only A's
// own bank question uses is deleted as before.
func TestDeletingAFileAnotherOwnersContentUsesIsRefusedWithACount(t *testing.T) {
	ctx := context.Background()
	w := newReplacementWorld(t, nil)
	file := w.asset(t, w.a, domain.KindImage, 1)
	question, _ := w.sharedQuestion(t, w.a, file, domain.KindImage, w.b)

	own := w.asset(t, w.a, domain.KindImage, 1)
	w.question(t, w.a, own, domain.KindImage, nil)
	if err := w.deleteAs(w.a, own); err != nil {
		t.Fatalf("A deleting a file only A's own bank question uses: %v", err)
	}

	if _, err := w.copyAs(ctx, w.b, question); err != nil {
		t.Fatal(err)
	}
	elsewhere := func(want int) {
		t.Helper()
		err := w.deleteAs(w.a, file)
		var used *domain.InUseElsewhereError
		if !errors.As(err, &used) || !errors.Is(err, domain.ErrInUseElsewhere) || errors.Is(err, domain.ErrReferenced) {
			t.Fatalf("A deleting the file B's copy uses: %v, want in use elsewhere", err)
		}
		if used.Count != want {
			t.Errorf("in use elsewhere counts %d, want %d", used.Count, want)
		}
	}
	elsewhere(1)
	w.stimulus(t, w.group(t, w.b, nil), file)
	elsewhere(2)
	w.versionUsing(t, w.b, file)
	elsewhere(3)

	mine := w.versionUsing(t, w.a, file)
	err := w.deleteAs(w.a, file)
	var referenced *domain.ReferencedError
	if !errors.As(err, &referenced) {
		t.Fatalf("A deleting the file A's own version uses: %v, want referenced", err)
	}
	if len(referenced.Tests) != 1 || referenced.Tests[0].ID != mine || len(referenced.Groups) != 0 {
		t.Errorf("the refusal names %+v and %+v; want A's test alone and none of B's", referenced.Tests, referenced.Groups)
	}
	if n := w.scalar(t, `SELECT count(*) FROM app.media_assets WHERE id = $1 AND deleted_at IS NULL`, file); n != 1 {
		t.Error("a refused delete deleted the file")
	}
}

// TestTheLibraryNamesOnlyTheOwnersOwnVersions: once B publishes a test of
// B's that uses A's file (a copy's, T-R7.7), A's library row counts B's
// version beside A's own but names A's alone, and A's replace answers the
// old file as still used by A's version alone, its removal held for B's.
// No title of B's test reaches A.
func TestTheLibraryNamesOnlyTheOwnersOwnVersions(t *testing.T) {
	ctx := context.Background()
	w := newReplacementWorld(t, nil)
	file := w.asset(t, w.a, domain.KindImage, 1)
	mine := w.versionUsing(t, w.a, file)
	theirs := w.versionUsing(t, w.b, file)
	w.exec(t, `UPDATE app.tests SET title = 'Đề riêng của B' WHERE id = $1`, theirs)

	listed, err := w.media().Queries.List.Handle(ctx, query.List{Input: domain.ListInput{Scope: access.Scope{UserID: w.a}, Limit: 100}})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, a := range listed.Items {
		if a.ID != file {
			continue
		}
		found = true
		if a.UsageCount != 2 || len(a.UsedIn) != 1 || a.UsedIn[0].ID != mine {
			t.Errorf("A's library row counts %d versions and names %+v; want both counted and A's test alone named", a.UsageCount, a.UsedIn)
		}
	}
	if !found {
		t.Fatalf("A's library does not list the file %s", file)
	}

	result, err := w.repo.Replace(ctx, w.input(file, w.a, domain.KindImage, 1, 1<<30))
	if err != nil {
		t.Fatal(err)
	}
	if used := result.Previous.UsedIn; len(used) != 1 || used[0].ID != mine {
		t.Errorf("A's replace answers the old file as used in %+v, want A's test alone", used)
	}
	if result.Previous.DeleteAt != nil {
		t.Error("the old file is due for removal although B's version still uses it")
	}
}

// TestAReplaceLeavesAnotherOwnersCopyOnTheOldFile: a copy is independent
// (D4), so A's replace re-points A's question and leaves B's copy on the old
// file, which stays readable to B, and the answer counts the copy as left.
func TestAReplaceLeavesAnotherOwnersCopyOnTheOldFile(t *testing.T) {
	ctx := context.Background()
	w := newReplacementWorld(t, nil)
	file := w.asset(t, w.a, domain.KindImage, 1)
	question, _ := w.sharedQuestion(t, w.a, file, domain.KindImage, w.b)
	copies, err := w.copyAs(ctx, w.b, question)
	if err != nil {
		t.Fatal(err)
	}

	result, err := w.repo.Replace(ctx, w.input(file, w.a, domain.KindImage, 1, 1<<30))
	if err != nil {
		t.Fatal(err)
	}
	if result.Repointed.Questions != 1 || result.Left.Questions != 1 || result.Left.Groups != 0 {
		t.Errorf("replace repointed %+v and left %+v; want A's question repointed and B's copy left", result.Repointed, result.Left)
	}
	if got := w.text(t, `SELECT media_asset_id::text FROM app.questions WHERE id = $1`, question); got != result.Asset.ID {
		t.Errorf("A's question names %s, want the new file %s", got, result.Asset.ID)
	}
	if got := w.text(t, `SELECT media_asset_id::text FROM app.questions WHERE id = $1`, copies[0]); got != file {
		t.Errorf("B's copy names %s, want the old file %s", got, file)
	}
	readable, err := repositories.Readable(ctx, w.pool, access.Scope{UserID: w.b}, []string{file})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := readable[file]; !ok {
		t.Error("B can no longer read the file B's copy names")
	}
}

// TestACopiersEditOfACopyKeepsTheOwnersAudio: B's copy of A's audio question
// binds A's audio, and B's later edit of the copy keeps it, even after A
// stops sharing: the copy is content B reads, so the media module reads the
// audio as B's to bind. C, who neither holds a share nor a copy, is refused
// the same audio as a missing file.
func TestACopiersEditOfACopyKeepsTheOwnersAudio(t *testing.T) {
	ctx := context.Background()
	w := newReplacementWorld(t, nil)
	audio := w.asset(t, w.a, domain.KindAudio, 1)
	repo := questionsrepo.NewPostgres(db.NewContext(w.pool)).WithMedia(adapters.QuestionMedia{})
	bank := questionsapp.New(repo, adapters.MediaKinds{Media: w.media()})
	input := func() questions.Input {
		return questions.Input{Type: questions.SingleChoice, Prompt: "Nghe và chọn", Points: "1", MediaAssetID: &audio,
			Audio:   &questions.AudioPolicy{AllowSeek: false, ShowTranscriptAfterSubmit: false},
			Options: []questions.OptionInput{{Text: "Một"}, {Text: "Hai", IsCorrect: true}}, Tags: []string{}}
	}
	original, err := bank.Commands.Create.Handle(ctx, questionscommand.Create{Request: questions.WriteRequest{Input: input(), ActorID: w.a}})
	if err != nil {
		t.Fatal(err)
	}
	test, section := w.section(t, w.a)
	w.exec(t, `INSERT INTO app.test_section_questions (test_section_id, ordinal, question_id) VALUES ($1, 0, $2)`, section, original.ID)
	w.exec(t, `INSERT INTO app.content_shares (kind, test_id, grantee_id, access, granted_by) VALUES ('test', $1, $2, 'use', $3)`, test, w.b, w.a)

	c := w.user(t)
	if _, err := bank.Commands.Create.Handle(ctx, questionscommand.Create{Request: questions.WriteRequest{Input: input(), ActorID: c}}); !errors.Is(err, questions.ErrMediaNotFound) {
		t.Errorf("C binding A's audio: %v, want the media not-found", err)
	}

	copies, err := w.copyAs(ctx, w.b, original.ID)
	if err != nil {
		t.Fatal(err)
	}
	w.exec(t, `DELETE FROM app.content_shares WHERE test_id = $1`, test)
	edited := input()
	edited.Prompt = "Nghe lại và chọn"
	saved, err := bank.Commands.Update.Handle(ctx, questionscommand.Update{Request: questions.WriteRequest{ID: copies[0], Input: edited, ActorID: w.b}})
	if err != nil {
		t.Fatalf("B editing B's copy that binds A's audio: %v", err)
	}
	if saved.MediaAssetID == nil || *saved.MediaAssetID != audio || saved.Prompt != edited.Prompt {
		t.Errorf("B's edit saved %v %q, want A's audio and the new prompt", saved.MediaAssetID, saved.Prompt)
	}
	if got := w.text(t, `SELECT prompt FROM app.questions WHERE id = $1`, original.ID); got != "Nghe và chọn" {
		t.Errorf("B's edit of the copy changed A's question to %q", got)
	}
}

// TestACopyAndADeleteOfItsFileSerialise drives B's copy of A's question and
// A's delete of the file it binds at the same moment, as
// TestLockForVersionUseSerialisesAgainstDelete drives a publish: the copy
// locks the file (LockForVersionUse) before it names it, and the delete
// reads who uses it under the same lock, so either the copy fails on the
// deleted file or the delete is refused by the copy; never both succeed with
// a copy naming a deleted file.
func TestACopyAndADeleteOfItsFileSerialise(t *testing.T) {
	ctx := context.Background()
	w := newReplacementWorld(t, nil)
	for attempt := range 8 {
		file := w.asset(t, w.a, domain.KindImage, 1)
		question, _ := w.sharedQuestion(t, w.a, file, domain.KindImage, w.b)

		var wg sync.WaitGroup
		var deleteErr, copyErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			deleteErr = w.deleteAs(w.a, file)
		}()
		go func() {
			defer wg.Done()
			_, copyErr = w.copyAs(ctx, w.b, question)
		}()
		wg.Wait()

		switch {
		case deleteErr == nil && copyErr == nil:
			t.Fatalf("attempt %d: the file was deleted AND a copy names it", attempt)
		case deleteErr == nil && !errors.Is(copyErr, questions.ErrMediaNotFound):
			t.Fatalf("attempt %d: the delete won, and the copy failed with %v, want the media not-found", attempt, copyErr)
		case copyErr == nil && !errors.Is(deleteErr, domain.ErrInUseElsewhere):
			t.Fatalf("attempt %d: the copy won, and the delete failed with %v, want in use elsewhere", attempt, deleteErr)
		}
		if n := w.scalar(t, `SELECT count(*) FROM app.questions q JOIN app.media_assets a ON a.id = q.media_asset_id
			WHERE q.owner_id = $1 AND a.deleted_at IS NOT NULL`, w.b); n != 0 {
			t.Fatalf("attempt %d: %d of B's questions name a deleted file", attempt, n)
		}
	}
}
