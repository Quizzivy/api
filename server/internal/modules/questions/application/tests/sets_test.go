//go:build integration

package application_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sync"
	"testing"

	"github.com/google/uuid"

	"quizzivy/internal/core/adapters"
	mediaapp "quizzivy/internal/modules/media/application"
	mediarepo "quizzivy/internal/modules/media/repositories"
	"quizzivy/internal/modules/questions/application"
	"quizzivy/internal/modules/questions/application/command"
	"quizzivy/internal/modules/questions/application/query"
	"quizzivy/internal/modules/questions/domain"
	"quizzivy/internal/modules/questions/repositories"
	"quizzivy/internal/platform/db"
	"quizzivy/internal/shared/access"
)

func newSetWorld(t *testing.T) *bankWorld {
	t.Helper()
	w := newBankWorld(t)
	repo := repositories.NewPostgres(db.NewContext(w.tx))
	kinds := adapters.MediaKinds{Media: mediaapp.New(mediarepo.NewPostgres(db.NewContext(w.tx)), nil, nil)}
	w.svc = application.New(repo, kinds).WithSets(repo, repo)
	return w
}

func setWriteBy(actor string, all bool, id string) domain.SetWrite {
	return domain.SetWrite{ID: id, ActorID: actor, All: all}
}

func (w *bankWorld) makeSet(t *testing.T, owner, name string, ids ...string) domain.QuestionSet {
	t.Helper()
	set, err := w.svc.Commands.CreateSet.Handle(context.Background(), command.CreateSet{Write: setWriteBy(owner, false, ""), Name: name, QuestionIDs: ids})
	if err != nil {
		t.Fatalf("%s making the set %q: %v", owner, name, err)
	}
	return set
}

func (w *bankWorld) setQuestions(t *testing.T, scope access.Scope, setID string) ([]string, int) {
	t.Helper()
	ctx := context.Background()
	in := domain.ListInput{Scope: scope.Own(), SetID: setID, Limit: 100}
	listed, err := w.svc.Queries.List.Handle(ctx, query.List{Input: in, Reader: scope})
	if err != nil {
		t.Fatalf("listing the set %s: %v", setID, err)
	}
	counts, err := w.svc.Queries.Counts.Handle(ctx, query.Counts{Input: in})
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, len(listed.Items))
	for i, q := range listed.Items {
		out[i] = q.ID
		if q.Owner == nil || q.Owner.ID == "" || q.Owner.Name == "" {
			t.Errorf("the set's question %s names no owner: %+v", q.ID, q.Owner)
		}
	}
	return out, counts.Total
}

func (w *bankWorld) setAudits(t *testing.T, setID string) []string {
	t.Helper()
	rows, err := w.tx.Query(context.Background(), `SELECT action || ' ' || coalesce(diff::text, '') FROM app.audit_log
		 WHERE entity = 'question_set' AND entity_id = $1 ORDER BY id`, setID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var row string
		if err := rows.Scan(&row); err != nil {
			t.Fatal(err)
		}
		out = append(out, row)
	}
	return out
}

func diffOf(ids ...string) string {
	raw, _ := json.Marshal(ids)
	return string(raw)
}

func TestASetHoldsItsQuestionsInTheOrderGivenAndListsThemSo(t *testing.T) {
	w := newSetWorld(t)
	ctx := context.Background()
	q1, q2, q3 := w.create(t, w.a, "Câu một", nil), w.create(t, w.a, "Câu hai", nil), w.create(t, w.a, "Câu ba", nil)
	mine := access.Scope{UserID: w.a}

	set := w.makeSet(t, w.a, "  Ngữ pháp cơ bản  ", q3.ID, q1.ID)
	if set.Name != "Ngữ pháp cơ bản" || set.Access != domain.AccessOwner || set.OwnerID != w.a || set.OwnerName == "" || set.QuestionCount != 2 || set.SharedWith != 0 {
		t.Fatalf("the new set reads %+v", set)
	}
	if got, total := w.setQuestions(t, mine, set.ID); !slices.Equal(got, []string{q3.ID, q1.ID}) || total != 2 {
		t.Fatalf("the set lists %v (bank total %d), want q3 then q1", got, total)
	}

	added, err := w.svc.Commands.AddToSet.Handle(ctx, command.AddToSet{Write: setWriteBy(w.a, false, set.ID), QuestionIDs: []string{q2.ID, q1.ID}})
	if err != nil || added.QuestionCount != 3 {
		t.Fatalf("adding q2 and q1 again: %+v, %v", added, err)
	}
	if got, _ := w.setQuestions(t, mine, set.ID); !slices.Equal(got, []string{q3.ID, q1.ID, q2.ID}) {
		t.Fatalf("after the add the set lists %v, want q3, q1, q2: the add appends and ignores what it holds", got)
	}

	removed, err := w.svc.Commands.RemoveFromSet.Handle(ctx, command.RemoveFromSet{Write: setWriteBy(w.a, false, set.ID), QuestionIDs: []string{q1.ID, uuid.NewString()}})
	if err != nil || removed.QuestionCount != 2 {
		t.Fatalf("removing q1 and an id the set does not hold: %+v, %v", removed, err)
	}
	if got, total := w.setQuestions(t, mine, set.ID); !slices.Equal(got, []string{q3.ID, q2.ID}) || total != 2 {
		t.Fatalf("after the removal the set lists %v (bank total %d), want q3 then q2", got, total)
	}
	if _, err := w.get(mine, q1.ID); err != nil {
		t.Errorf("a question taken out of a set left the bank: %v", err)
	}

	renamed, err := w.svc.Commands.UpdateSet.Handle(ctx, command.UpdateSet{Write: setWriteBy(w.a, false, set.ID), Fields: domain.SetFields{
		Name: new("Ngữ pháp"), Description: new("Cho lớp tối"), DescriptionSet: true,
	}})
	if err != nil || renamed.Name != "Ngữ pháp" || renamed.Description == nil || *renamed.Description != "Cho lớp tối" {
		t.Fatalf("renaming the set: %+v, %v", renamed, err)
	}
	cleared, err := w.svc.Commands.UpdateSet.Handle(ctx, command.UpdateSet{Write: setWriteBy(w.a, false, set.ID), Fields: domain.SetFields{DescriptionSet: true}})
	if err != nil || cleared.Description != nil || cleared.Name != "Ngữ pháp" {
		t.Fatalf("clearing the description: %+v, %v", cleared, err)
	}

	sets, err := w.svc.Queries.ListSets.Handle(ctx, query.ListSets{Scope: mine})
	if err != nil || len(sets) != 1 || sets[0].ID != set.ID || sets[0].QuestionCount != 2 {
		t.Fatalf("A's sets are %+v (%v), want the one set with two questions", sets, err)
	}
	if theirs, err := w.svc.Queries.ListSets.Handle(ctx, query.ListSets{Scope: access.Scope{UserID: w.b}}); err != nil || len(theirs) != 0 {
		t.Errorf("B's sets are %+v (%v), want none", theirs, err)
	}
}

func TestADeletedQuestionLeavesItsSets(t *testing.T) {
	w := newSetWorld(t)
	ctx := context.Background()
	kept, gone := w.create(t, w.a, "Câu giữ lại", nil), w.create(t, w.a, "Câu bị xoá", nil)
	first, second := w.makeSet(t, w.a, "Bộ một", gone.ID, kept.ID), w.makeSet(t, w.a, "Bộ hai", gone.ID)

	if _, err := w.svc.Commands.Delete.Handle(ctx, command.Delete{Request: request(w.a, false, gone.ID, domain.Input{})}); err != nil {
		t.Fatalf("deleting a question two sets hold: %v", err)
	}
	if got, _ := w.setQuestions(t, access.Scope{UserID: w.a}, first.ID); !slices.Equal(got, []string{kept.ID}) {
		t.Errorf("the first set lists %v after the delete, want only the kept question", got)
	}
	var items int
	if err := w.tx.QueryRow(ctx, `SELECT count(*) FROM app.question_set_items WHERE question_id = $1`, gone.ID).Scan(&items); err != nil {
		t.Fatal(err)
	}
	if items != 0 {
		t.Errorf("%d set items still name the deleted question", items)
	}
	if set, err := w.svc.Queries.GetSet.Handle(ctx, query.GetSet{ID: second.ID, Scope: access.Scope{UserID: w.a}}); err != nil || set.QuestionCount != 0 {
		t.Errorf("the second set reads %+v (%v), want no questions", set, err)
	}
}

func TestAQuestionASetCannotHoldAnswersAsAMissingOne(t *testing.T) {
	w := newSetWorld(t)
	ctx := context.Background()
	mine := w.create(t, w.a, "Câu của A", nil)
	theirs := w.create(t, w.b, "Câu của B", nil)
	deleted := w.create(t, w.a, "Câu đã xoá", nil)
	if _, err := w.svc.Commands.Delete.Handle(ctx, command.Delete{Request: request(w.a, false, deleted.ID, domain.Input{})}); err != nil {
		t.Fatal(err)
	}
	group := w.id(t, `INSERT INTO app.question_groups (title, created_by, owner_id) VALUES ('Bài đọc', $1, $1) RETURNING id::text`, w.a)
	member := w.id(t, `INSERT INTO app.questions (type, prompt, points, created_by, owner_id, context_group_id, context_ordinal, context_option_order)
		VALUES ('short_answer', 'Câu trong bài đọc', 1, $1, $1, $2, 0, 'shuffle') RETURNING id::text`, w.a, group)
	missing := uuid.NewString()

	_, err := w.svc.Commands.CreateSet.Handle(ctx, command.CreateSet{Write: setWriteBy(w.a, false, ""), Name: "Bộ lẫn", QuestionIDs: []string{mine.ID, theirs.ID, deleted.ID, member, missing}})
	var refused *domain.QuestionsNotFoundError
	if !errors.As(err, &refused) || !errors.Is(err, domain.ErrNotFound) || !slices.Equal(refused.IDs, []string{theirs.ID, deleted.ID, member, missing}) {
		t.Fatalf("a set of A's, B's, a deleted, a group member's and a missing id answered %v (%+v)", err, refused)
	}
	var sets int
	if err := w.tx.QueryRow(ctx, `SELECT count(*) FROM app.question_sets WHERE owner_id = $1`, w.a).Scan(&sets); err != nil {
		t.Fatal(err)
	}
	if sets != 0 {
		t.Errorf("a refused create left %d sets", sets)
	}

	set := w.makeSet(t, w.a, "Bộ của A", mine.ID)
	_, err = w.svc.Commands.AddToSet.Handle(ctx, command.AddToSet{Write: setWriteBy(w.a, false, set.ID), QuestionIDs: []string{theirs.ID}})
	if !errors.As(err, &refused) || !slices.Equal(refused.IDs, []string{theirs.ID}) {
		t.Errorf("adding B's question to A's set answered %v", err)
	}

	for label, run := range map[string]func() error{
		"read": func() error {
			_, err := w.svc.Queries.GetSet.Handle(ctx, query.GetSet{ID: set.ID, Scope: access.Scope{UserID: w.b}})
			return err
		},
		"list": func() error {
			_, err := w.svc.Queries.List.Handle(ctx, query.List{Input: domain.ListInput{Scope: access.Scope{UserID: w.b}, SetID: set.ID}, Reader: access.Scope{UserID: w.b}})
			return err
		},
		"rename": func() error {
			_, err := w.svc.Commands.UpdateSet.Handle(ctx, command.UpdateSet{Write: setWriteBy(w.b, false, set.ID), Fields: domain.SetFields{Name: new("Đổi tên")}})
			return err
		},
		"add": func() error {
			_, err := w.svc.Commands.AddToSet.Handle(ctx, command.AddToSet{Write: setWriteBy(w.b, false, set.ID), QuestionIDs: []string{theirs.ID}})
			return err
		},
		"remove": func() error {
			_, err := w.svc.Commands.RemoveFromSet.Handle(ctx, command.RemoveFromSet{Write: setWriteBy(w.b, false, set.ID), QuestionIDs: []string{mine.ID}})
			return err
		},
		"delete": func() error {
			_, err := w.svc.Commands.DeleteSet.Handle(ctx, command.DeleteSet{Write: setWriteBy(w.b, false, set.ID)})
			return err
		},
		"edit in its context": func() error {
			_, err := w.svc.Commands.Update.Handle(ctx, command.Update{Request: request(w.b, false, mine.ID, w.input("Sửa", nil)), SetID: set.ID})
			return err
		},
	} {
		if err := run(); !errors.Is(err, domain.ErrSetNotFound) {
			t.Errorf("B's %s of A's set answered %v, want ErrSetNotFound", label, err)
		}
	}
	if got, _ := w.setQuestions(t, access.Scope{UserID: w.a}, set.ID); !slices.Equal(got, []string{mine.ID}) {
		t.Errorf("A's set lists %v after B's refused writes", got)
	}
	everyone := access.Scope{UserID: w.admin, All: true}
	if read, err := w.svc.Queries.GetSet.Handle(ctx, query.GetSet{ID: set.ID, Scope: everyone}); err != nil || read.Access != domain.AccessOwner || read.OwnerID != w.a {
		t.Errorf("the Admin reads A's set as %+v (%v), want it with owner access", read, err)
	}
}

func TestADeletedSetAnswersAsAMissingOneAndTakesItsGrants(t *testing.T) {
	w := newSetWorld(t)
	ctx := context.Background()
	q := w.create(t, w.a, "Câu trong bộ", nil)
	set := w.makeSet(t, w.a, "Bộ sẽ xoá", q.ID)
	if _, err := w.tx.Exec(ctx, `INSERT INTO app.content_shares (kind, question_set_id, grantee_id, access, granted_by)
		VALUES ('question_set', $1, $2, 'use', $3)`, set.ID, w.b, w.a); err != nil {
		t.Fatal(err)
	}
	if read, err := w.svc.Queries.GetSet.Handle(ctx, query.GetSet{ID: set.ID, Scope: access.Scope{UserID: w.a}}); err != nil || read.SharedWith != 1 {
		t.Fatalf("the shared set reads %+v (%v), want one grant counted", read, err)
	}
	if _, err := w.svc.Commands.DeleteSet.Handle(ctx, command.DeleteSet{Write: setWriteBy(w.a, false, set.ID)}); err != nil {
		t.Fatalf("deleting the set: %v", err)
	}
	var grants int
	if err := w.tx.QueryRow(ctx, `SELECT count(*) FROM app.content_shares WHERE question_set_id = $1`, set.ID).Scan(&grants); err != nil {
		t.Fatal(err)
	}
	if grants != 0 {
		t.Errorf("%d grants survived the set's deletion", grants)
	}
	if _, err := w.get(access.Scope{UserID: w.a}, q.ID); err != nil {
		t.Errorf("the deleted set's question left the bank: %v", err)
	}
	mine := access.Scope{UserID: w.a}
	if _, err := w.svc.Queries.GetSet.Handle(ctx, query.GetSet{ID: set.ID, Scope: mine}); !errors.Is(err, domain.ErrSetNotFound) {
		t.Errorf("reading a deleted set answered %v", err)
	}
	if _, err := w.svc.Queries.List.Handle(ctx, query.List{Input: domain.ListInput{Scope: mine, SetID: set.ID}, Reader: mine}); !errors.Is(err, domain.ErrSetNotFound) {
		t.Errorf("listing a deleted set's questions answered %v", err)
	}
	if _, err := w.svc.Commands.AddToSet.Handle(ctx, command.AddToSet{Write: setWriteBy(w.a, false, set.ID), QuestionIDs: []string{q.ID}}); !errors.Is(err, domain.ErrSetNotFound) {
		t.Errorf("adding to a deleted set answered %v", err)
	}
	if _, err := w.svc.Commands.DeleteSet.Handle(ctx, command.DeleteSet{Write: setWriteBy(w.a, false, set.ID)}); !errors.Is(err, domain.ErrSetNotFound) {
		t.Errorf("deleting the set again answered %v", err)
	}
	if sets, err := w.svc.Queries.ListSets.Handle(ctx, query.ListSets{Scope: mine}); err != nil || len(sets) != 0 {
		t.Errorf("A's sets list %+v (%v) after the delete", sets, err)
	}
}

func TestEachSetWriteIsAuditedWithWhatChanged(t *testing.T) {
	w := newSetWorld(t)
	ctx := context.Background()
	q1, q2 := w.create(t, w.a, "Câu một", nil), w.create(t, w.a, "Câu hai", nil)
	set := w.makeSet(t, w.a, "Bộ ghi nhật ký", q1.ID)
	by := setWriteBy(w.a, false, set.ID)
	for _, run := range []func() error{
		func() error {
			_, err := w.svc.Commands.UpdateSet.Handle(ctx, command.UpdateSet{Write: by, Fields: domain.SetFields{Name: new("Bộ đã đổi tên")}})
			return err
		},
		func() error {
			_, err := w.svc.Commands.UpdateSet.Handle(ctx, command.UpdateSet{Write: by, Fields: domain.SetFields{Name: new("Bộ đã đổi tên")}})
			return err
		},
		func() error {
			_, err := w.svc.Commands.AddToSet.Handle(ctx, command.AddToSet{Write: by, QuestionIDs: []string{q2.ID}})
			return err
		},
		func() error {
			_, err := w.svc.Commands.AddToSet.Handle(ctx, command.AddToSet{Write: by, QuestionIDs: []string{q2.ID}})
			return err
		},
		func() error {
			_, err := w.svc.Commands.RemoveFromSet.Handle(ctx, command.RemoveFromSet{Write: by, QuestionIDs: []string{q1.ID}})
			return err
		},
		func() error {
			_, err := w.svc.Commands.RemoveFromSet.Handle(ctx, command.RemoveFromSet{Write: by, QuestionIDs: []string{q1.ID}})
			return err
		},
		func() error {
			_, err := w.svc.Commands.DeleteSet.Handle(ctx, command.DeleteSet{Write: by})
			return err
		},
	} {
		if err := run(); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{
		`question_set.created {"added": ` + diffOf(q1.ID) + `}`,
		`question_set.updated {"changed": {"name": {"new": "Bộ đã đổi tên", "old": "Bộ ghi nhật ký"}}}`,
		`question_set.updated {"added": ` + diffOf(q2.ID) + `}`,
		`question_set.updated {"removed": ` + diffOf(q1.ID) + `}`,
		`question_set.deleted `,
	}
	if got := w.setAudits(t, set.ID); !slices.Equal(got, want) {
		t.Errorf("the set's audit rows are\n  %q\nwant\n  %q\n(a write that changes nothing writes no row)", got, want)
	}
}

func TestASetHoldsAtMostFiveHundredQuestions(t *testing.T) {
	w := newSetWorld(t)
	ctx := context.Background()
	rows, err := w.tx.Query(ctx, `INSERT INTO app.questions (type, prompt, points, created_by, owner_id)
		SELECT 'short_answer', 'Câu ' || n, 1, $1, $1 FROM generate_series(1, 501) AS n RETURNING id::text`, w.a)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	rows.Close()
	set := w.makeSet(t, w.a, "Bộ đầy", ids[:500]...)
	if set.QuestionCount != 500 {
		t.Fatalf("the full set holds %d", set.QuestionCount)
	}
	_, err = w.svc.Commands.AddToSet.Handle(ctx, command.AddToSet{Write: setWriteBy(w.a, false, set.ID), QuestionIDs: ids[500:]})
	if !errors.Is(err, domain.ErrSetFull) {
		t.Errorf("a 501st question answered %v, want ErrSetFull", err)
	}
	if again, err := w.svc.Commands.AddToSet.Handle(ctx, command.AddToSet{Write: setWriteBy(w.a, false, set.ID), QuestionIDs: ids[:1]}); err != nil || again.QuestionCount != 500 {
		t.Errorf("adding a question the full set holds: %+v, %v", again, err)
	}
}

func TestAQuestionMadeForATestBelongsToItsOwner(t *testing.T) {
	w := newSetWorld(t)
	ctx := context.Background()
	theirTest := w.id(t, `INSERT INTO app.tests (title, created_by, owner_id) VALUES ('Đề của B', $1, $1) RETURNING id::text`, w.b)
	myTest := w.id(t, `INSERT INTO app.tests (title, created_by, owner_id) VALUES ('Đề của A', $1, $1) RETURNING id::text`, w.a)

	for label, testID := range map[string]string{"B's test": theirTest, "a missing test": uuid.NewString()} {
		_, err := w.svc.Commands.Create.Handle(ctx, command.Create{Request: request(w.a, false, "", w.input("Câu cho đề", nil)), ForTestID: testID})
		if !errors.Is(err, domain.ErrTestNotFound) {
			t.Errorf("A making a question for %s answered %v, want ErrTestNotFound", label, err)
		}
	}
	made, err := w.svc.Commands.Create.Handle(ctx, command.Create{Request: request(w.a, false, "", w.input("Câu cho đề của A", nil)), ForTestID: myTest})
	if err != nil {
		t.Fatal(err)
	}
	if owner, _, _ := w.owner(t, made.ID); owner != w.a {
		t.Errorf("A's question for A's test is owned by %s", owner)
	}
	byAdmin, err := w.svc.Commands.Duplicate.Handle(ctx, command.Duplicate{Request: request(w.admin, true, made.ID, domain.Input{}), ForTestID: theirTest})
	if err != nil {
		t.Fatal(err)
	}
	var owner, author string
	if err := w.tx.QueryRow(ctx, `SELECT owner_id::text, created_by::text FROM app.questions WHERE id = $1`, byAdmin.ID).Scan(&owner, &author); err != nil {
		t.Fatal(err)
	}
	if owner != w.b || author != w.admin {
		t.Errorf("the Admin's copy for B's test is owned by %s and made by %s, want B and the Admin", owner, author)
	}
}

func TestAnEditInASetsContextNeedsTheSetToHoldTheQuestion(t *testing.T) {
	w := newSetWorld(t)
	ctx := context.Background()
	held, other := w.create(t, w.a, "Câu trong bộ", nil), w.create(t, w.a, "Câu ngoài bộ", nil)
	set := w.makeSet(t, w.a, "Bộ sửa", held.ID)
	edited, err := w.svc.Commands.Update.Handle(ctx, command.Update{Request: request(w.a, false, held.ID, w.input("Câu đã sửa", nil)), SetID: set.ID})
	if err != nil || edited.ID != held.ID || edited.Prompt != "Câu đã sửa" {
		t.Fatalf("the owner's edit in the set's context: %+v, %v", edited, err)
	}
	if _, err := w.svc.Commands.Update.Handle(ctx, command.Update{Request: request(w.a, false, other.ID, w.input("Sửa", nil)), SetID: set.ID}); !errors.Is(err, domain.ErrSetNotFound) {
		t.Errorf("an edit in the context of a set that does not hold the question answered %v", err)
	}
}

// TestAddingToASetSerialisesAgainstDelete drives an add to a set and the
// question's delete at once, as TestLockForDraftUseSerialisesAgainstDelete
// does for a draft: no set may end holding a deleted question.
func TestAddingToASetSerialisesAgainstDelete(t *testing.T) {
	pool := newPool(t)
	author := makeAuthor(t, pool)
	repo := repositories.NewPostgres(db.NewContext(pool))
	svc := application.New(repo, mediaKinds{pool}).WithSets(repo, repo)
	ctx := context.Background()
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM app.question_sets WHERE owner_id = $1`, author); err != nil {
			t.Errorf("removing the sets: %v", err)
		}
	})
	anchor := write(t, svc, author, "Câu giữ bộ")
	set, err := svc.Commands.CreateSet.Handle(ctx, command.CreateSet{Write: setWriteBy(author, false, ""), Name: "Bộ tranh chấp", QuestionIDs: []string{anchor.ID}})
	if err != nil {
		t.Fatal(err)
	}
	for attempt := range 8 {
		q := write(t, svc, author, "Câu hỏi tranh chấp")
		var wg sync.WaitGroup
		var deleteErr, addErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, deleteErr = svc.Commands.Delete.Handle(ctx, command.Delete{Request: domain.WriteRequest{ID: q.ID, ActorID: author}})
		}()
		go func() {
			defer wg.Done()
			_, addErr = svc.Commands.AddToSet.Handle(ctx, command.AddToSet{Write: setWriteBy(author, false, set.ID), QuestionIDs: []string{q.ID}})
		}()
		wg.Wait()
		if deleteErr != nil {
			t.Fatalf("attempt %d: the delete failed with %v; a set never refuses it", attempt, deleteErr)
		}
		if addErr != nil && !errors.Is(addErr, domain.ErrNotFound) {
			t.Fatalf("attempt %d: the add failed with %v, want success or a missing question", attempt, addErr)
		}
		var held int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM app.question_set_items i JOIN app.questions q ON q.id = i.question_id
			 WHERE i.set_id = $1 AND q.deleted_at IS NOT NULL`, set.ID).Scan(&held); err != nil {
			t.Fatal(err)
		}
		if held != 0 {
			t.Fatalf("attempt %d: the set holds %d deleted questions", attempt, held)
		}
	}
}

func TestAGrantOpensTheSetToItsGranteeAtItsLevel(t *testing.T) {
	w := newSetWorld(t)
	ctx := context.Background()
	user := w.user(t, "teacher")
	q1, q2 := w.create(t, w.a, "Câu một", nil), w.create(t, w.a, "Câu hai", nil)
	set := w.makeSet(t, w.a, "Bộ dùng chung", q1.ID, q2.ID)
	for grantee, level := range map[string]string{w.b: "edit", user: "use"} {
		if _, err := w.tx.Exec(ctx, `INSERT INTO app.content_shares (kind, question_set_id, grantee_id, access, granted_by)
			VALUES ('question_set', $1::uuid, $2::uuid, $3, $4::uuid)`, set.ID, grantee, level, w.a); err != nil {
			t.Fatal(err)
		}
	}

	for grantee, want := range map[string]domain.Access{w.b: domain.AccessEdit, user: domain.AccessUse} {
		got, err := w.svc.Queries.GetSet.Handle(ctx, query.GetSet{Scope: access.Scope{UserID: grantee}, ID: set.ID})
		if err != nil || got.Access != want || got.OwnerID != w.a || got.SharedWith != 0 {
			t.Fatalf("the %s grantee reads %+v, %v", want, got, err)
		}
		if listed, _ := w.setQuestions(t, access.Scope{UserID: grantee}, set.ID); !slices.Equal(listed, []string{q1.ID, q2.ID}) {
			t.Errorf("the %s grantee lists the set's questions as %v", want, listed)
		}
		if mine, err := w.svc.Queries.ListSets.Handle(ctx, query.ListSets{Scope: access.Scope{UserID: grantee}}); err != nil || len(mine) != 0 {
			t.Errorf("the %s grantee's own sets hold %+v (%v): a shared set is listed by Shared with me", want, mine, err)
		}
	}
	if owned, err := w.svc.Queries.GetSet.Handle(ctx, query.GetSet{Scope: access.Scope{UserID: w.a}, ID: set.ID}); err != nil || owned.Access != domain.AccessOwner || owned.SharedWith != 2 {
		t.Fatalf("the owner reads %+v, %v", owned, err)
	}

	rename := domain.SetFields{Name: new("Bộ đã đổi tên")}
	if _, err := w.svc.Commands.UpdateSet.Handle(ctx, command.UpdateSet{Write: setWriteBy(user, false, set.ID), Fields: rename}); !errors.Is(err, domain.ErrSetReadOnly) {
		t.Errorf("a use grantee renaming the set: %v, want read only", err)
	}
	if renamed, err := w.svc.Commands.UpdateSet.Handle(ctx, command.UpdateSet{Write: setWriteBy(w.b, false, set.ID), Fields: rename}); err != nil || renamed.Name != "Bộ đã đổi tên" || renamed.Access != domain.AccessEdit {
		t.Errorf("an edit grantee renaming the set: %+v, %v", renamed, err)
	}
	if _, err := w.svc.Commands.AddToSet.Handle(ctx, command.AddToSet{Write: setWriteBy(w.b, false, set.ID), QuestionIDs: []string{q1.ID}}); !errors.Is(err, domain.ErrSetOwnerOnly) {
		t.Errorf("an edit grantee adding to the set: %v, want owner only", err)
	}
	if _, err := w.svc.Commands.DeleteSet.Handle(ctx, command.DeleteSet{Write: setWriteBy(w.b, false, set.ID)}); !errors.Is(err, domain.ErrSetOwnerOnly) {
		t.Errorf("an edit grantee deleting the set: %v, want owner only", err)
	}
	if removed, err := w.svc.Commands.RemoveFromSet.Handle(ctx, command.RemoveFromSet{Write: setWriteBy(w.b, false, set.ID), QuestionIDs: []string{q2.ID}}); err != nil || removed.QuestionCount != 1 {
		t.Errorf("an edit grantee removing from the set: %+v, %v", removed, err)
	}
	if _, err := w.svc.Queries.GetSet.Handle(ctx, query.GetSet{Scope: access.Scope{UserID: w.user(t, "teacher")}, ID: set.ID}); !errors.Is(err, domain.ErrSetNotFound) {
		t.Errorf("a teacher with no grant reads the set: %v", err)
	}
}
