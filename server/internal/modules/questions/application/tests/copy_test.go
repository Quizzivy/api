//go:build integration

package application_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"quizzivy/internal/modules/questions/application/command"
	"quizzivy/internal/modules/questions/application/query"
	"quizzivy/internal/modules/questions/domain"
	testscommand "quizzivy/internal/modules/tests/application/command"
	testsquery "quizzivy/internal/modules/tests/application/query"
	testsdomain "quizzivy/internal/modules/tests/domain"
	"quizzivy/internal/shared/access"
	"quizzivy/internal/shared/content"
)

// gapInput is a fill-blank question whose prompt places its two gaps by id.
func gapInput() domain.Input {
	a, b := "a", "b"
	prompt := json.RawMessage(`{"format":"semantic_v1","blocks":[{"type":"table","rows":[[{"header":false,"rowSpan":1,"colSpan":1,"content":[{"type":"paragraph","content":[{"type":"gap","id":"a","label":"1"}]}]},{"header":false,"rowSpan":1,"colSpan":1,"content":[{"type":"paragraph","content":[{"type":"gap","id":"b","label":"2"}]}]}]]}]}`)
	return domain.Input{Type: domain.FillBlank, Prompt: "[1]\t[2]", PromptContent: prompt, Points: "2.00", Tags: []string{},
		Blanks: []domain.BlankInput{{GapID: &a, Ordinal: 1, AcceptedAnswers: []string{"đi"}}, {GapID: &b, Ordinal: 2, AcceptedAnswers: []string{"ngủ", "nghỉ"}, CaseSensitive: true}}}
}

func essayInput() domain.Input {
	return domain.Input{Tags: []string{}, Type: domain.Essay, Prompt: "Viết về thành phố của bạn.", Points: "9",
		Rubric: []domain.RubricCriterionInput{{Label: "Task response"}, {Label: "Coherence"}}}
}

// draftOf is a test of owner's whose one section holds questions in order.
func (w *forkWorld) draftOf(t *testing.T, owner string, questions ...string) string {
	t.Helper()
	test := w.id(t, `INSERT INTO app.tests (title, created_by, owner_id) VALUES ('Đề để sao chép', $1, $1) RETURNING id::text`, owner)
	section := w.section(t, test, 0)
	for i, id := range questions {
		w.hold(t, "test_section_questions", section, i, id)
	}
	return test
}

func (w *forkWorld) copyTest(actor, testID string) (testsdomain.Test, error) {
	return w.tests.Commands.Copy.Handle(context.Background(), testscommand.Copy{Request: testsdomain.Request{ID: testID, ActorID: actor, Scope: access.Scope{UserID: actor}}})
}

func (w *forkWorld) questionCopies(actor, testID string, ids ...string) ([]string, error) {
	return w.tests.Commands.CopyQuestions.Handle(context.Background(), testscommand.CopyQuestions{Copies: testsdomain.QuestionCopies{
		Request: testsdomain.Request{ID: testID, ActorID: actor, Scope: access.Scope{UserID: actor}}, QuestionIDs: ids}})
}

func (w *forkWorld) copySet(actor, setID string, ids []string) (domain.SetCopy, error) {
	return w.svc.Commands.CopySet.Handle(context.Background(), command.CopySet{Write: setWriteBy(actor, false, setID), QuestionIDs: ids})
}

// copiedAudits is the share.copied rows about entity id, each its actor and
// the source it names.
func (w *forkWorld) copiedAudits(t *testing.T, entity, id string) []string {
	t.Helper()
	rows, err := w.tx.Query(context.Background(), `SELECT actor_user_id::text || ' ' || (diff->>'sourceId') FROM app.audit_log
		WHERE action = 'share.copied' AND entity = $1 AND entity_id = $2`, entity, id)
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

// sameChildren holds copy to source's content: the same prompt and texts in
// new rows of its own, a matching item keyed to the copy's option in the same
// place, the same rubric, and blanks under fresh gap ids that its prompt
// places as the source's prompt places the source's.
func sameChildren(t *testing.T, source, copy domain.Question) {
	t.Helper()
	if copy.Prompt != source.Prompt || copy.Type != source.Type || copy.Points != source.Points {
		t.Errorf("the copy reads %s %q %s, want %s %q %s", copy.Type, copy.Prompt, copy.Points, source.Type, source.Prompt, source.Points)
	}
	if len(copy.Options) != len(source.Options) || len(copy.MatchItems) != len(source.MatchItems) ||
		len(copy.Rubric) != len(source.Rubric) || len(copy.Blanks) != len(source.Blanks) {
		t.Fatalf("the copy holds %d options, %d items, %d criteria, %d blanks; want %d, %d, %d, %d", len(copy.Options), len(copy.MatchItems), len(copy.Rubric), len(copy.Blanks),
			len(source.Options), len(source.MatchItems), len(source.Rubric), len(source.Blanks))
	}
	for i, option := range copy.Options {
		if option.ID == source.Options[i].ID || option.Text != source.Options[i].Text || option.IsCorrect != source.Options[i].IsCorrect {
			t.Errorf("the copy's option %d is %+v, want a new row like %+v", i, option, source.Options[i])
		}
	}
	for i, item := range copy.MatchItems {
		want := source.MatchItems[i]
		if item.ID == want.ID || item.Text != want.Text || item.CorrectOption != want.CorrectOption || item.CorrectOptionID != copy.Options[item.CorrectOption].ID {
			t.Errorf("the copy's item %d is %+v, want a new row like %+v keyed to the copy's option %d", i, item, want, want.CorrectOption)
		}
	}
	for i, criterion := range copy.Rubric {
		if criterion.ID == source.Rubric[i].ID || criterion.Label != source.Rubric[i].Label {
			t.Errorf("the copy's criterion %d is %+v, want a new row like %+v", i, criterion, source.Rubric[i])
		}
	}
	if len(copy.Blanks) == 0 {
		return
	}
	document, err := content.ParsePromptWithMedia(copy.PromptContent)
	if err != nil {
		t.Fatal(err)
	}
	placed := document.GapIDs()
	for i, blank := range copy.Blanks {
		want := source.Blanks[i]
		if blank.ID == want.ID || blank.GapID == nil || *blank.GapID == *want.GapID || !slices.Equal(blank.AcceptedAnswers, want.AcceptedAnswers) || blank.CaseSensitive != want.CaseSensitive {
			t.Errorf("the copy's blank %d is %+v, want a new row like %+v under a fresh gap id", i, blank, want)
		}
		if blank.GapID != nil && !slices.Contains(placed, *blank.GapID) {
			t.Errorf("the copy's blank %d names gap %s, which its prompt does not place (%v)", i, *blank.GapID, placed)
		}
	}
}

// TestACopyOfASharedTestMakesTheCopiersOwnQuestions: B, with Can use, copies
// A's test whose draft holds a matching question, an essay with a rubric and
// a fill-blank question. Each becomes a question of B's own bank, created by
// B, with its own options, items keyed to them, criteria and fresh gap ids,
// in the draft's order. Editing either side then leaves the other unchanged.
func TestACopyOfASharedTestMakesTheCopiersOwnQuestions(t *testing.T) {
	w := newForkWorld(t)
	ctx := context.Background()
	sources := []domain.Question{w.createWith(t, w.a, matchingInput()), w.createWith(t, w.a, essayInput()), w.createWith(t, w.a, gapInput())}
	test := w.draftOf(t, w.a, sources[0].ID, sources[1].ID, sources[2].ID)
	w.grantTest(t, test, w.b, "use")

	copied, err := w.copyTest(w.b, test)
	if err != nil {
		t.Fatalf("B copying A's test: %v", err)
	}
	if copied.OwnerID != w.b || copied.Access != testsdomain.AccessOwner || copied.ID == test {
		t.Fatalf("the copy reads owner %s access %s id %s, want B's own new test", copied.OwnerID, copied.Access, copied.ID)
	}
	held := w.draftHolds(t, copied.ID)
	rows, err := w.tx.Query(ctx, `SELECT sq.question_id::text FROM app.test_section_questions sq JOIN app.test_sections s ON s.id = sq.test_section_id
		WHERE s.test_id = $1 ORDER BY s.ordinal, sq.ordinal`, copied.ID)
	if err != nil {
		t.Fatal(err)
	}
	var ordered []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ordered = append(ordered, id)
	}
	rows.Close()
	if len(held) != 3 || len(ordered) != 3 {
		t.Fatalf("the copy's draft holds %v, want three questions", held)
	}
	for i, id := range ordered {
		if slices.ContainsFunc(sources, func(q domain.Question) bool { return q.ID == id }) {
			t.Fatalf("the copy's draft names A's question %s", id)
		}
		owner, createdBy, updatedBy, _ := w.row(t, id)
		if owner != w.b || createdBy != w.b || updatedBy != w.b {
			t.Errorf("the copy %s is owned by %s, created by %s, last written by %s; want B for all three", id, owner, createdBy, updatedBy)
		}
		copy, err := w.get(access.Scope{UserID: w.b}, id)
		if err != nil {
			t.Fatal(err)
		}
		sameChildren(t, sources[i], copy)
	}
	if got := w.copiedAudits(t, "test", copied.ID); !slices.Equal(got, []string{w.b + " " + test}) {
		t.Errorf("the copy's share.copied rows are %v, want one by B from A's test", got)
	}

	edit := essayInput()
	edit.Prompt = "B viết lại đề"
	if _, err := w.svc.Commands.Update.Handle(ctx, command.Update{Request: request(w.b, false, ordered[1], edit)}); err != nil {
		t.Fatalf("B editing B's copy: %v", err)
	}
	if _, _, _, prompt := w.row(t, sources[1].ID); prompt != sources[1].Prompt {
		t.Errorf("B's edit of the copy changed A's question to %q", prompt)
	}
	edit.Prompt = "A viết lại đề"
	if _, err := w.svc.Commands.Update.Handle(ctx, command.Update{Request: request(w.a, false, sources[1].ID, edit)}); err != nil {
		t.Fatal(err)
	}
	if _, _, _, prompt := w.row(t, ordered[1]); prompt != "B viết lại đề" {
		t.Errorf("A's edit of the original changed B's copy to %q", prompt)
	}
}

// TestCopyingATestNeedsOnlyToReadIt: anyone who reads the test copies it,
// its owner included, who gets copies too rather than references; anyone who
// cannot is answered as for a missing test.
func TestCopyingATestNeedsOnlyToReadIt(t *testing.T) {
	w := newForkWorld(t)
	q := w.create(t, w.a, "Câu của A", nil)
	test := w.draftOf(t, w.a, q.ID)
	if _, err := w.copyTest(w.b, test); !errors.Is(err, testsdomain.ErrNotFound) {
		t.Errorf("B copying A's test without a grant: %v, want not found", err)
	}
	mine, err := w.copyTest(w.a, test)
	if err != nil {
		t.Fatal(err)
	}
	if held := w.draftHolds(t, mine.ID); len(held) != 1 || held[0] == q.ID {
		t.Errorf("A's own copy holds %v, want one new question, not %s", held, q.ID)
	}
	w.grantTest(t, test, w.b, "edit")
	if _, err := w.copyTest(w.b, test); err != nil {
		t.Errorf("B copying with Can edit: %v", err)
	}
	w.revokeTest(t, test, w.b)
	if _, err := w.copyTest(w.b, test); !errors.Is(err, testsdomain.ErrNotFound) {
		t.Errorf("B copying after the grant was revoked: %v, want not found", err)
	}
}

// TestAWholeSetCopyIsANewSetOfFreshQuestions: B, with Can use, copies A's
// set: a new set of B's, named as A's, holding a copy of each question in
// set order, audited as copied from A's set. A's set is unchanged.
func TestAWholeSetCopyIsANewSetOfFreshQuestions(t *testing.T) {
	w := newForkWorld(t)
	q1, q2 := w.createWith(t, w.a, matchingInput()), w.create(t, w.a, "Câu hai", nil)
	set := w.makeSet(t, w.a, "Bộ của A", q2.ID, q1.ID)
	w.grantSet(t, set.ID, w.b, "use")

	copied, err := w.copySet(w.b, set.ID, nil)
	if err != nil {
		t.Fatalf("B copying A's set: %v", err)
	}
	if copied.SetID == nil || *copied.SetID == set.ID || len(copied.QuestionIDs) != 2 {
		t.Fatalf("the copy is %+v, want a new set and two questions", copied)
	}
	mine, err := w.svc.Queries.GetSet.Handle(context.Background(), query.GetSet{ID: *copied.SetID, Scope: access.Scope{UserID: w.b}})
	if err != nil || mine.OwnerID != w.b || mine.Name != "Bộ của A" || mine.Access != domain.AccessOwner || mine.SharedWith != 0 {
		t.Fatalf("B's new set reads %+v, %v; want B's own, named as A's, shared with no one", mine, err)
	}
	if got := w.setHolds(t, *copied.SetID); !slices.Equal(got, copied.QuestionIDs) {
		t.Errorf("B's set holds %v, want the copies %v in order", got, copied.QuestionIDs)
	}
	for i, source := range []domain.Question{q2, q1} {
		owner, createdBy, _, _ := w.row(t, copied.QuestionIDs[i])
		if copied.QuestionIDs[i] == source.ID || owner != w.b || createdBy != w.b {
			t.Errorf("copy %d is %s owned by %s, created by %s; want a new question of B's", i, copied.QuestionIDs[i], owner, createdBy)
		}
		copy, err := w.get(access.Scope{UserID: w.b}, copied.QuestionIDs[i])
		if err != nil {
			t.Fatal(err)
		}
		sameChildren(t, source, copy)
	}
	if got := w.setHolds(t, set.ID); !slices.Equal(got, []string{q2.ID, q1.ID}) {
		t.Errorf("A's set holds %v after the copy, want q2 then q1", got)
	}
	if got := w.copiedAudits(t, "question_set", *copied.SetID); !slices.Equal(got, []string{w.b + " " + set.ID}) {
		t.Errorf("the set's share.copied rows are %v, want one by B from A's set", got)
	}
}

// TestAPartialSetCopyCopiesTheChosenQuestionsInSetOrder: with questionIds,
// B gets copies of just those in the order the set holds them, in B's bank,
// and no set; an id the set does not hold is named as missing, and a set B
// cannot read is a missing set.
func TestAPartialSetCopyCopiesTheChosenQuestionsInSetOrder(t *testing.T) {
	w := newForkWorld(t)
	q1, q2, q3 := w.create(t, w.a, "Câu một", nil), w.create(t, w.a, "Câu hai", nil), w.create(t, w.a, "Câu ba", nil)
	set := w.makeSet(t, w.a, "Bộ của A", q1.ID, q2.ID, q3.ID)
	outside := w.create(t, w.a, "Câu ngoài bộ", nil)

	if _, err := w.copySet(w.b, set.ID, []string{q1.ID}); !errors.Is(err, domain.ErrSetNotFound) {
		t.Errorf("B copying from A's set without a grant: %v, want the set not found", err)
	}
	w.grantSet(t, set.ID, w.b, "use")
	_, err := w.copySet(w.b, set.ID, []string{q1.ID, outside.ID})
	var missing *domain.QuestionsNotFoundError
	if !errors.As(err, &missing) || !slices.Equal(missing.IDs, []string{outside.ID}) {
		t.Errorf("copying a question the set does not hold: %v, want it named as missing", err)
	}

	copied, err := w.copySet(w.b, set.ID, []string{q3.ID, q1.ID})
	if err != nil {
		t.Fatal(err)
	}
	if copied.SetID != nil || len(copied.QuestionIDs) != 2 {
		t.Fatalf("the partial copy is %+v, want two questions and no set", copied)
	}
	for i, source := range []domain.Question{q1, q3} {
		owner, createdBy, _, prompt := w.row(t, copied.QuestionIDs[i])
		if owner != w.b || createdBy != w.b || prompt != source.Prompt {
			t.Errorf("copy %d is owned by %s, created by %s, reads %q; want B's copy of %q", i, owner, createdBy, prompt, source.Prompt)
		}
		if got := w.copiedAudits(t, "question", copied.QuestionIDs[i]); !slices.Equal(got, []string{w.b + " " + source.ID}) {
			t.Errorf("copy %d's share.copied rows are %v, want one by B from %s", i, got, source.ID)
		}
	}
	var sets int
	if err := w.tx.QueryRow(context.Background(), `SELECT count(*) FROM app.question_sets WHERE owner_id = $1`, w.b).Scan(&sets); err != nil || sets != 0 {
		t.Errorf("B owns %d sets after a partial copy (%v), want none", sets, err)
	}
}

// TestQuestionCopiesForATestLandInTheTestsOwnersBank: B, with Can edit on
// A's test, copies B's own question and A's into A's bank, created by B. The
// copies pass R2's rule on the next autosave, where B's own question is
// refused. Can use is read only, no grant a missing test, and a question B
// cannot read is named as missing.
func TestQuestionCopiesForATestLandInTheTestsOwnersBank(t *testing.T) {
	w := newForkWorld(t)
	ctx := context.Background()
	theirs := w.create(t, w.a, "Câu của A", nil)
	test := w.draftOf(t, w.a, theirs.ID)
	mine := w.create(t, w.b, "Câu của B", nil)
	stranger := w.user(t, "teacher")
	strangers := w.create(t, stranger, "Câu của người khác", nil)

	if _, err := w.questionCopies(w.b, test, mine.ID); !errors.Is(err, testsdomain.ErrNotFound) {
		t.Errorf("without a grant: %v, want the test not found", err)
	}
	w.grantTest(t, test, w.b, "use")
	if _, err := w.questionCopies(w.b, test, mine.ID); !errors.Is(err, testsdomain.ErrReadOnly) {
		t.Errorf("with Can use: %v, want read only", err)
	}
	w.revokeTest(t, test, w.b)
	w.grantTest(t, test, w.b, "edit")
	_, err := w.questionCopies(w.b, test, mine.ID, strangers.ID)
	var missing *domain.QuestionsNotFoundError
	if !errors.As(err, &missing) || !slices.Equal(missing.IDs, []string{strangers.ID}) {
		t.Errorf("copying a question B cannot read: %v, want it named as missing", err)
	}

	copies, err := w.questionCopies(w.b, test, mine.ID, theirs.ID)
	if err != nil {
		t.Fatalf("B copying for A's test: %v", err)
	}
	for i, source := range []domain.Question{mine, theirs} {
		owner, createdBy, _, prompt := w.row(t, copies[i])
		if copies[i] == source.ID || owner != w.a || createdBy != w.b || prompt != source.Prompt {
			t.Errorf("copy %d is %s owned by %s, created by %s, reads %q; want a new question of A's made by B", i, copies[i], owner, createdBy, prompt)
		}
		if got := w.copiedAudits(t, "question", copies[i]); !slices.Equal(got, []string{w.b + " " + source.ID}) {
			t.Errorf("copy %d's share.copied rows are %v", i, got)
		}
	}

	autosave := func(ids ...string) error {
		current, err := w.tests.Queries.Get.Handle(ctx, testsquery.Get{ID: test, Scope: access.Scope{UserID: w.b}})
		if err != nil {
			return err
		}
		_, err = w.tests.Commands.Update.Handle(ctx, testscommand.Update{Request: testsdomain.Request{ID: test, ActorID: w.b, Scope: access.Scope{UserID: w.b}},
			Input: testsdomain.UpdateInput{ExpectedUpdatedAt: current.UpdatedAt, SetSections: true,
				Sections: []testsdomain.SectionInput{{ID: current.Sections[0].ID, Title: current.Sections[0].Title, QuestionIDs: ids}}}})
		return err
	}
	if err := autosave(theirs.ID, mine.ID); !errors.Is(err, testsdomain.ErrUnknownQuestion) {
		t.Errorf("B's autosave naming B's own question in A's draft: %v, want R2's unknown question", err)
	}
	if err := autosave(theirs.ID, copies[0], copies[1]); err != nil {
		t.Errorf("B's autosave naming the copies: %v", err)
	}
}
