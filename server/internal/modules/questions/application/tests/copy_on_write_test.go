//go:build integration

package application_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	"quizzivy/internal/core/adapters"
	mediaapp "quizzivy/internal/modules/media/application"
	mediarepo "quizzivy/internal/modules/media/repositories"
	"quizzivy/internal/modules/questions/application"
	"quizzivy/internal/modules/questions/application/command"
	"quizzivy/internal/modules/questions/domain"
	"quizzivy/internal/modules/questions/repositories"
	testsapp "quizzivy/internal/modules/tests/application"
	testscommand "quizzivy/internal/modules/tests/application/command"
	testsdomain "quizzivy/internal/modules/tests/domain"
	testsrepo "quizzivy/internal/modules/tests/repositories"
	"quizzivy/internal/platform/db"
	"quizzivy/internal/shared/access"
)

// forkWorld is the bank, its sets and the tests module wired as production
// wires them, on one transaction: A owns the questions, B is the colleague A
// shares tests and sets with.
type forkWorld struct {
	*bankWorld
	tests *testsapp.Application
}

func newForkWorld(t *testing.T) *forkWorld {
	t.Helper()
	w := newBankWorld(t)
	dbx := db.NewContext(w.tx)
	repo := repositories.NewPostgres(dbx).WithMedia(adapters.QuestionMedia{})
	tests := testsapp.New(testsrepo.NewPostgres(dbx, repo, mediarepo.NewPostgres(dbx)).
		WithGroupQuestions(adapters.GroupQuestions{}).WithQuestionWrites(adapters.QuestionWrites{}))
	kinds := adapters.MediaKinds{Media: mediaapp.New(mediarepo.NewPostgres(dbx), nil, nil)}
	w.svc = application.New(repo, kinds).WithSets(repo, repo).WithTests(adapters.EditableTests{Tests: tests.Queries.Ownership})
	return &forkWorld{bankWorld: w, tests: tests}
}

func (w *forkWorld) grantSet(t *testing.T, setID, grantee, level string) {
	t.Helper()
	if _, err := w.tx.Exec(context.Background(), `INSERT INTO app.content_shares (kind, question_set_id, grantee_id, access, granted_by)
		VALUES ('question_set', $1::uuid, $2::uuid, $3, (SELECT owner_id FROM app.question_sets WHERE id = $1::uuid))`, setID, grantee, level); err != nil {
		t.Fatal(err)
	}
}

// editInTest is editTestQuestion by actor: the question's new prompt, in the
// test testID's context.
func (w *forkWorld) editInTest(actor string, all bool, testID, questionID string, in domain.Input) (testsdomain.QuestionEdited, error) {
	return w.tests.Commands.EditQuestion.Handle(context.Background(), testscommand.EditQuestion{Edit: testsdomain.QuestionEdit{
		Request:    testsdomain.Request{ID: testID, ActorID: actor, Scope: access.Scope{UserID: actor, All: all}},
		QuestionID: questionID, Input: in,
	}})
}

// draftHolds is the bank questions the test's draft names, in id order.
func (w *forkWorld) draftHolds(t *testing.T, testID string) []string {
	t.Helper()
	rows, err := w.tx.Query(context.Background(), `SELECT sq.question_id::text FROM app.test_section_questions sq
		JOIN app.test_sections s ON s.id = sq.test_section_id WHERE s.test_id = $1 ORDER BY 1`, testID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	return ids
}

// setHolds is the set's items as question id and ordinal, in ordinal order.
func (w *forkWorld) setHolds(t *testing.T, setID string) []string {
	t.Helper()
	rows, err := w.tx.Query(context.Background(), `SELECT question_id::text FROM app.question_set_items WHERE set_id = $1 ORDER BY ordinal`, setID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	return ids
}

// row is who owns, made and last wrote a question, and its prompt.
func (w *forkWorld) row(t *testing.T, id string) (owner, createdBy, updatedBy, prompt string) {
	t.Helper()
	if err := w.tx.QueryRow(context.Background(), `SELECT owner_id::text, created_by::text, coalesce(updated_by::text, ''), prompt
		FROM app.questions WHERE id = $1`, id).Scan(&owner, &createdBy, &updatedBy, &prompt); err != nil {
		t.Fatal(err)
	}
	return owner, createdBy, updatedBy, prompt
}

func (w *forkWorld) forkAudits(t *testing.T, to string) []map[string]any {
	t.Helper()
	rows, err := w.tx.Query(context.Background(), `SELECT actor_user_id::text, diff FROM app.audit_log
		WHERE action = 'question.forked' AND entity = 'question' AND entity_id = $1`, to)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var actor string
		var diff json.RawMessage
		if err := rows.Scan(&actor, &diff); err != nil {
			t.Fatal(err)
		}
		entry := map[string]any{}
		if err := json.Unmarshal(diff, &entry); err != nil {
			t.Fatal(err)
		}
		entry["actor"] = actor
		out = append(out, entry)
	}
	return out
}

// TestAnEditGranteesEditOfAQuestionUsedElsewhereForksItForTheirTest is the
// exit criterion: A's question Q is used by A's test T1, shared with B as Can
// edit, by A's test T2 and by A's set S1. B's edit of Q in T1 makes Q′,
// owned by A and created by B, and only T1 is pointed at it: T2 and S1 still
// use Q, unchanged.
func TestAnEditGranteesEditOfAQuestionUsedElsewhereForksItForTheirTest(t *testing.T) {
	w := newForkWorld(t)
	q := w.create(t, w.a, "Câu gốc của A", nil)
	t1, t2 := w.draftUsing(t, w.a, q.ID), w.draftUsing(t, w.a, q.ID)
	s1 := w.makeSet(t, w.a, "Bộ S1", q.ID)
	w.grantTest(t, t1, w.b, "edit")

	edited, err := w.editInTest(w.b, false, t1, q.ID, w.input("Câu B đã sửa", nil))
	if err != nil {
		t.Fatalf("B's edit of Q in T1: %v", err)
	}
	fork := edited.Question
	if !edited.Forked || fork.ID == q.ID || fork.Prompt != "Câu B đã sửa" {
		t.Fatalf("B's edit answered forked=%v %s %q, want a fork carrying the edit", edited.Forked, fork.ID, fork.Prompt)
	}
	if fork.Access != domain.AccessEdit || fork.Owner == nil || fork.Owner.ID != w.a {
		t.Errorf("the fork reads to B as %q owned by %+v, want edit and A", fork.Access, fork.Owner)
	}
	if owner, made, wrote, _ := w.row(t, fork.ID); owner != w.a || made != w.b || wrote != w.b {
		t.Errorf("Q′ is owned by %s, made by %s, last written by %s; want A, B, B", owner, made, wrote)
	}
	if got := w.draftHolds(t, t1); !slices.Equal(got, []string{fork.ID}) {
		t.Errorf("T1 holds %v, want only Q′", got)
	}
	if got := w.draftHolds(t, t2); !slices.Equal(got, []string{q.ID}) {
		t.Errorf("T2 holds %v, want Q still", got)
	}
	if got := w.setHolds(t, s1.ID); !slices.Equal(got, []string{q.ID}) {
		t.Errorf("S1 holds %v, want Q still", got)
	}
	if _, _, _, prompt := w.row(t, q.ID); prompt != "Câu gốc của A" {
		t.Errorf("Q reads %q after B's fork, want it unchanged", prompt)
	}
	foreign, err := repositories.NotOwnedBy(context.Background(), w.tx, w.a, w.draftHolds(t, t1))
	if err != nil || len(foreign) != 0 {
		t.Errorf("R2's owner check refuses %v (%v) in T1: the fork must be the test owner's", foreign, err)
	}

	var by string
	var at time.Time
	if err := w.tx.QueryRow(context.Background(), `SELECT updated_by::text, updated_at FROM app.tests WHERE id = $1`, t1).Scan(&by, &at); err != nil {
		t.Fatal(err)
	}
	if by != w.b || !at.Equal(edited.TestUpdatedAt) {
		t.Errorf("T1 was last written by %s at %v, the edit answered %v; want B at the answer's time", by, at, edited.TestUpdatedAt)
	}
	audits := w.forkAudits(t, fork.ID)
	want := map[string]any{"actor": w.b, "from": q.ID, "to": fork.ID, "context": map[string]any{"kind": "test", "id": t1}}
	if len(audits) != 1 || !jsonEqual(audits[0], want) {
		t.Errorf("question.forked audits %+v, want one %+v", audits, want)
	}
}

func jsonEqual(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

// TestAnEditGranteesEditOfAQuestionNothingElseUsesIsMadeInPlace: with T1 the
// only use of Q, B's edit changes Q itself, which stays A's.
func TestAnEditGranteesEditOfAQuestionNothingElseUsesIsMadeInPlace(t *testing.T) {
	w := newForkWorld(t)
	q := w.create(t, w.a, "Câu chỉ ở T1", nil)
	t1 := w.draftUsing(t, w.a, q.ID)
	w.grantTest(t, t1, w.b, "edit")

	edited, err := w.editInTest(w.b, false, t1, q.ID, w.input("Câu B sửa tại chỗ", nil))
	if err != nil {
		t.Fatalf("B's edit: %v", err)
	}
	if edited.Forked || edited.Question.ID != q.ID || edited.Question.Prompt != "Câu B sửa tại chỗ" {
		t.Fatalf("B's edit answered forked=%v %s %q, want Q edited in place", edited.Forked, edited.Question.ID, edited.Question.Prompt)
	}
	if owner, made, wrote, prompt := w.row(t, q.ID); owner != w.a || made != w.a || wrote != w.b || prompt != "Câu B sửa tại chỗ" {
		t.Errorf("Q is %s/%s/%s %q, want A's, made by A, last written by B, edited", owner, made, wrote, prompt)
	}
	if got := w.draftHolds(t, t1); !slices.Equal(got, []string{q.ID}) {
		t.Errorf("T1 holds %v, want Q", got)
	}
	var forks int
	if err := w.tx.QueryRow(context.Background(), `SELECT count(*) FROM app.questions WHERE owner_id = $1`, w.a).Scan(&forks); err != nil {
		t.Fatal(err)
	}
	if forks != 1 {
		t.Errorf("A's bank holds %d questions after an in-place edit, want 1", forks)
	}
}

// TestTheOwnersEditInTheirTestChangesTheQuestionEverywhere: A's own edit of Q
// in T1 never forks, so T2 sees it, as before R7.
func TestTheOwnersEditInTheirTestChangesTheQuestionEverywhere(t *testing.T) {
	w := newForkWorld(t)
	q := w.create(t, w.a, "Câu dùng chung", nil)
	t1, t2 := w.draftUsing(t, w.a, q.ID), w.draftUsing(t, w.a, q.ID)
	s1 := w.makeSet(t, w.a, "Bộ S1", q.ID)
	w.grantTest(t, t1, w.b, "edit")

	edited, err := w.editInTest(w.a, false, t1, q.ID, w.input("A sửa ở mọi nơi", nil))
	if err != nil {
		t.Fatalf("A's edit: %v", err)
	}
	if edited.Forked || edited.Question.ID != q.ID || edited.Question.Access != domain.AccessOwner {
		t.Fatalf("A's edit answered forked=%v %s %q, want Q edited in place for its owner", edited.Forked, edited.Question.ID, edited.Question.Access)
	}
	for name, holds := range map[string][]string{"T1": w.draftHolds(t, t1), "T2": w.draftHolds(t, t2), "S1": w.setHolds(t, s1.ID)} {
		if !slices.Equal(holds, []string{q.ID}) {
			t.Errorf("%s holds %v, want Q", name, holds)
		}
	}
	if got, err := w.get(access.Scope{UserID: w.a}, q.ID); err != nil || got.Prompt != "A sửa ở mọi nơi" {
		t.Errorf("T2's Q reads %q (%v), want A's edit", got.Prompt, err)
	}

	// scope.all outside any context edits in place too.
	admin, err := w.svc.Commands.Update.Handle(context.Background(), command.Update{Request: request(w.admin, true, q.ID, w.input("Quản trị sửa", nil))})
	if err != nil || admin.ID != q.ID {
		t.Errorf("the Admin's bank edit answered %s, %v, want Q in place", admin.ID, err)
	}
}

// TestAnEditInASharedSetForksForThatSetOnly: B edits Q in S2, a set A shares
// with B as Can edit, while A's own set S1 also holds Q. S2's item now holds
// Q′ at the same place, the answer is Q′, and S1 still holds Q. With S2 the
// only use, B's edit is made in place.
func TestAnEditInASharedSetForksForThatSetOnly(t *testing.T) {
	w := newForkWorld(t)
	ctx := context.Background()
	first, q := w.create(t, w.a, "Câu đầu", nil), w.create(t, w.a, "Câu trong hai bộ", nil)
	s1 := w.makeSet(t, w.a, "Bộ S1", q.ID)
	s2 := w.makeSet(t, w.a, "Bộ S2", first.ID, q.ID)
	w.grantSet(t, s2.ID, w.b, "edit")

	fork, err := w.svc.Commands.Update.Handle(ctx, command.Update{Request: request(w.b, false, q.ID, w.input("B sửa trong S2", nil)), SetID: s2.ID})
	if err != nil {
		t.Fatalf("B's edit in S2: %v", err)
	}
	if fork.ID == q.ID || fork.Prompt != "B sửa trong S2" || fork.Access != domain.AccessEdit {
		t.Fatalf("B's edit in S2 answered %s %q %q, want the fork", fork.ID, fork.Prompt, fork.Access)
	}
	if got := w.setHolds(t, s2.ID); !slices.Equal(got, []string{first.ID, fork.ID}) {
		t.Errorf("S2 holds %v, want the first question then Q′ in Q's place", got)
	}
	if got := w.setHolds(t, s1.ID); !slices.Equal(got, []string{q.ID}) {
		t.Errorf("S1 holds %v, want Q", got)
	}
	if owner, made, _, prompt := w.row(t, fork.ID); owner != w.a || made != w.b || prompt != "B sửa trong S2" {
		t.Errorf("Q′ is %s/%s %q, want A's, made by B", owner, made, prompt)
	}
	if _, _, _, prompt := w.row(t, q.ID); prompt != "Câu trong hai bộ" {
		t.Errorf("Q reads %q after the fork, want it unchanged", prompt)
	}
	var by string
	if err := w.tx.QueryRow(ctx, `SELECT updated_by::text FROM app.question_sets WHERE id = $1`, s2.ID).Scan(&by); err != nil || by != w.b {
		t.Errorf("S2 was last written by %s (%v), want B", by, err)
	}
	audits := w.forkAudits(t, fork.ID)
	if len(audits) != 1 || !jsonEqual(audits[0]["context"], map[string]any{"kind": "set", "id": s2.ID}) {
		t.Errorf("question.forked audits %+v, want one in S2's context", audits)
	}

	again, err := w.svc.Commands.Update.Handle(ctx, command.Update{Request: request(w.b, false, fork.ID, w.input("B sửa Q′ lần nữa", nil)), SetID: s2.ID})
	if err != nil || again.ID != fork.ID {
		t.Errorf("B's second edit of Q′, which only S2 uses, answered %s, %v, want Q′ in place", again.ID, err)
	}
}

// TestAnEditInContextIsRefusedAsTheRuleSays: a Can use grantee is read only, a
// question the test's draft does not hold answers as a missing one, a stale
// revision is refused naming the last writer with nothing written, and an
// archived test must be restored first.
func TestAnEditInContextIsRefusedAsTheRuleSays(t *testing.T) {
	w := newForkWorld(t)
	ctx := context.Background()
	q, outside := w.create(t, w.a, "Câu trong đề", nil), w.create(t, w.a, "Câu ngoài đề", nil)
	t1, t2 := w.draftUsing(t, w.a, q.ID), w.draftUsing(t, w.a, q.ID)
	use := w.user(t, "teacher")
	w.grantTest(t, t1, w.b, "edit")
	w.grantTest(t, t1, use, "use")

	if _, err := w.editInTest(use, false, t1, q.ID, w.input("Sửa", nil)); !errors.Is(err, testsdomain.ErrReadOnly) {
		t.Errorf("a Can use grantee's edit answered %v, want read only", err)
	}
	if _, err := w.editInTest(w.user(t, "teacher"), false, t1, q.ID, w.input("Sửa", nil)); !errors.Is(err, testsdomain.ErrNotFound) {
		t.Errorf("a stranger's edit answered %v, want the test missing", err)
	}
	for name, id := range map[string]string{"outside the draft": outside.ID, "B's own": w.create(t, w.b, "Câu của B", nil).ID} {
		if _, err := w.editInTest(w.b, false, t1, id, w.input("Sửa", nil)); !errors.Is(err, testsdomain.ErrQuestionNotInDraft) {
			t.Errorf("B's edit of a question %s answered %v, want not in the draft", name, err)
		}
	}

	stale := q.UpdatedAt.Add(-time.Second)
	in := w.input("Sửa cũ", nil)
	_, err := w.tests.Commands.EditQuestion.Handle(ctx, testscommand.EditQuestion{Edit: testsdomain.QuestionEdit{
		Request: testsdomain.Request{ID: t1, ActorID: w.b, Scope: access.Scope{UserID: w.b}}, QuestionID: q.ID, Input: in, ExpectedUpdatedAt: &stale,
	}})
	if !errors.Is(err, domain.ErrStaleWrite) {
		t.Fatalf("a stale fork answered %v, want STALE_WRITE", err)
	}
	if got := w.draftHolds(t, t1); !slices.Equal(got, []string{q.ID}) {
		t.Errorf("a refused fork left T1 holding %v", got)
	}
	var questions int
	if err := w.tx.QueryRow(ctx, `SELECT count(*) FROM app.questions WHERE owner_id = $1`, w.a).Scan(&questions); err != nil || questions != 2 {
		t.Errorf("A's bank holds %d questions after a refused fork (%v), want 2", questions, err)
	}
	current := q.UpdatedAt
	_, err = w.tests.Commands.EditQuestion.Handle(ctx, testscommand.EditQuestion{Edit: testsdomain.QuestionEdit{
		Request: testsdomain.Request{ID: t1, ActorID: w.b, Scope: access.Scope{UserID: w.b}}, QuestionID: q.ID, Input: in, ExpectedUpdatedAt: &current,
	}})
	if err != nil {
		t.Errorf("a fork from the current revision answered %v", err)
	}

	if _, err := w.tx.Exec(ctx, `UPDATE app.tests SET status = 'archived' WHERE id = $1`, t2); err != nil {
		t.Fatal(err)
	}
	if _, err := w.editInTest(w.a, false, t2, q.ID, w.input("Sửa", nil)); !errors.Is(err, testsdomain.ErrArchived) {
		t.Errorf("an edit in an archived test answered %v, want TEST_ARCHIVED", err)
	}
}

// TestAForkKeepsTheMediaTheQuestionBinds: B's edit in context may keep A's
// image, which reading Q through T1 authorized, and attach B's own, but not a
// third teacher's, which answers as a missing asset does.
func TestAForkKeepsTheMediaTheQuestionBinds(t *testing.T) {
	w := newForkWorld(t)
	image, mine, theirs := w.image(t, w.a), w.image(t, w.b), w.image(t, w.user(t, "teacher"))
	q := w.create(t, w.a, "Câu có hình của A", &image)
	t1 := w.draftUsing(t, w.a, q.ID)
	_ = w.draftUsing(t, w.a, q.ID)
	w.grantTest(t, t1, w.b, "edit")

	edited, err := w.editInTest(w.b, false, t1, q.ID, w.input("B giữ hình của A", &image))
	if err != nil || !edited.Forked || edited.Question.MediaAssetID == nil || *edited.Question.MediaAssetID != image {
		t.Fatalf("B's fork keeping A's image answered %+v, %v", edited.Question.MediaAssetID, err)
	}
	fork := edited.Question.ID
	if _, err := w.editInTest(w.b, false, t1, fork, w.input("B dùng hình của mình", &mine)); err != nil {
		t.Errorf("B attaching B's own image: %v", err)
	}
	if _, err := w.editInTest(w.b, false, t1, fork, w.input("B dùng hình người khác", &theirs)); !errors.Is(err, domain.ErrMediaNotFound) {
		t.Errorf("B attaching a third teacher's image answered %v, want a missing asset", err)
	}
}
