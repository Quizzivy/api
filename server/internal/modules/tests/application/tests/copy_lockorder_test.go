//go:build integration

package application_test

import (
	"context"
	"testing"
	"time"

	"quizzivy/internal/core/adapters"
	mediarepo "quizzivy/internal/modules/media/repositories"
	"quizzivy/internal/modules/tests/domain"
	"quizzivy/internal/modules/tests/repositories"
	"quizzivy/internal/platform/db"
	"quizzivy/internal/shared/access"
)

// TestACopyLocksAllItsMediaInOneSortedPass: a copy of a test whose
// standalone question plays the higher-ordered file and whose section group
// plays the lower-ordered one takes the lower file's lock first, as a
// publish or a group save would, and so holds neither while it waits for the
// lower. Locking the questions' files and then the groups' in two sorted
// passes would hold the higher while it waited for the lower, which
// deadlocks against a publish of a test that uses both (40P01).
//
// Another transaction holds the lower file's lock; once the copy waits on
// it, the higher must still be free (FOR UPDATE NOWAIT); then the copy
// completes.
func TestACopyLocksAllItsMediaInOneSortedPass(t *testing.T) {
	ctx := context.Background()
	pool := newPool(t)
	author := pubMakeAuthor(t, pool)
	t.Cleanup(func() {
		// The section groups go first, so the author's own clean-up can
		// remove the tests and the files they play.
		for _, sql := range []string{
			`DELETE FROM app.test_section_units WHERE test_section_id IN
				(SELECT s.id FROM app.test_sections s JOIN app.tests t ON t.id = s.test_id WHERE t.created_by = $1)`,
			`DELETE FROM app.group_gap_bindings WHERE group_id IN (SELECT id FROM app.question_groups WHERE created_by = $1)`,
			`DELETE FROM app.questions WHERE context_group_id IN (SELECT id FROM app.question_groups WHERE created_by = $1)`,
			`DELETE FROM app.question_groups WHERE created_by = $1`,
		} {
			if _, err := pool.Exec(context.Background(), sql, author); err != nil {
				t.Errorf("clean up %q: %v", sql, err)
			}
		}
	})
	b := newBuilder(t, pool, author)
	scope := access.Scope{UserID: author}

	low, high := audioAsset(t, pool, author, "copy-order-1"), audioAsset(t, pool, author, "copy-order-2")
	if high < low {
		low, high = high, low
	}
	draft := b.draft("Đề sao chép", b.listeningQuestion("Nghe tệp sau", high))
	media := mediarepo.NewPostgres(db.NewContext(pool))
	tests := repositories.NewPostgres(db.NewContext(pool), adapters.GroupQuestions{}, media).
		WithGroupQuestions(adapters.GroupQuestions{}).WithQuestionWrites(adapters.QuestionWrites{})
	current, err := tests.Get(ctx, scope, draft.ID)
	if err != nil {
		t.Fatal(err)
	}
	groups := repositories.NewGroupsPostgres(db.NewContext(pool), adapters.GroupQuestions{}, media)
	if _, err := groups.Create(ctx, domain.CreateGroupInput{Bundle: storedGroupFixture(t, low), OwnerSectionID: &current.Sections[0].ID,
		ExpectedTestUpdatedAt: current.UpdatedAt, ActorID: author, Now: time.Now(), Scope: scope, Grants: bothKeys}); err != nil {
		t.Fatal(err)
	}

	holder, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = holder.Rollback(ctx) }()
	var holderPID int
	if err := holder.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&holderPID); err != nil {
		t.Fatal(err)
	}
	if _, err := holder.Exec(ctx, `SELECT 1 FROM app.media_assets WHERE id = $1 FOR UPDATE`, low); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := tests.Copy(ctx, domain.DuplicateInput{ID: draft.ID, ActorID: author, Scope: scope, Now: time.Now()})
		done <- err
	}()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var waiting int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE $1::int = ANY(pg_blocking_pids(pid))`, holderPID).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting > 0 {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("the copy finished (%v) without waiting for the lower file's lock", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("the copy never waited for the lower file's lock")
		}
		time.Sleep(20 * time.Millisecond)
	}

	probe, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, probeErr := probe.Exec(ctx, `SELECT 1 FROM app.media_assets WHERE id = $1 FOR UPDATE NOWAIT`, high)
	_ = probe.Rollback(ctx)
	_ = holder.Rollback(ctx)
	copyErr := <-done

	if probeErr != nil {
		t.Errorf("the copy held the higher file while it waited for the lower (%v): it locks its groups' media after its questions'", probeErr)
	}
	if copyErr != nil {
		t.Fatalf("the copy failed once the lock was released: %v", copyErr)
	}
}
