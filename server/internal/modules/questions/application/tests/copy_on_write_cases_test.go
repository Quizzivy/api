//go:build integration

package application_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"testing"

	"quizzivy/internal/modules/questions/application/command"
	"quizzivy/internal/modules/questions/domain"
	"quizzivy/internal/shared/access"
	"quizzivy/internal/shared/validation"
)

func (w *forkWorld) audio(t *testing.T, owner string) string {
	t.Helper()
	return w.id(t, `INSERT INTO app.media_assets (kind, storage_key, mime_type, bytes, duration_ms, original_filename, checksum_sha256, uploaded_by, owner_id)
		VALUES ('audio', $1, 'audio/mpeg', 1, 1000, 'nghe.mp3', sha256(convert_to($1, 'UTF8')), $2, $2) RETURNING id::text`, "fork/"+t.Name()+"/"+w.marker, owner)
}

func (w *forkWorld) createWith(t *testing.T, actor string, in domain.Input) domain.Question {
	t.Helper()
	q, err := w.svc.Commands.Create.Handle(context.Background(), command.Create{Request: request(actor, false, "", in)})
	if err != nil {
		t.Fatalf("%s creating %q: %v", actor, in.Prompt, err)
	}
	return q
}

// section is a new section of the test at ordinal.
func (w *forkWorld) section(t *testing.T, testID string, ordinal int) string {
	t.Helper()
	return w.id(t, `INSERT INTO app.test_sections (test_id, ordinal, title) VALUES ($1, $2, $3) RETURNING id::text`,
		testID, ordinal, fmt.Sprintf("Phần %d", ordinal+1))
}

// hold makes the section hold the question at ordinal in table, the legacy
// outline's test_section_questions or the group-aware test_section_units.
func (w *forkWorld) hold(t *testing.T, table, sectionID string, ordinal int, questionID string) {
	t.Helper()
	if _, err := w.tx.Exec(context.Background(), `INSERT INTO app.`+table+` (test_section_id, ordinal, question_id) VALUES ($1, $2, $3)`,
		sectionID, ordinal, questionID); err != nil {
		t.Fatal(err)
	}
}

// outline is every row of the test's draft that names a question, in both
// tables, as "table section-ordinal/ordinal question".
func (w *forkWorld) outline(t *testing.T, testID string) []string {
	t.Helper()
	rows, err := w.tx.Query(context.Background(), `
		SELECT 'questions', s.ordinal, r.ordinal, r.question_id::text FROM app.test_section_questions r
		  JOIN app.test_sections s ON s.id = r.test_section_id WHERE s.test_id = $1
		UNION ALL
		SELECT 'units', s.ordinal, r.ordinal, r.question_id::text FROM app.test_section_units r
		  JOIN app.test_sections s ON s.id = r.test_section_id WHERE s.test_id = $1 AND r.question_id IS NOT NULL
		ORDER BY 1, 2, 3`, testID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var table, question string
		var section, ordinal int
		if err := rows.Scan(&table, &section, &ordinal, &question); err != nil {
			t.Fatal(err)
		}
		out = append(out, fmt.Sprintf("%s %d/%d %s", table, section, ordinal, question))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func (w *forkWorld) bankSize(t *testing.T, owner string) int {
	t.Helper()
	var n int
	if err := w.tx.QueryRow(context.Background(), `SELECT count(*) FROM app.questions WHERE owner_id = $1`, owner).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestAForkKeepsWhatTheEditLeavesOut: an edit that forks is held to the
// original as an in-place update is. An option sent without its content keeps
// the original option's formatting, or is refused as options[i].content when
// its text changed, and an audio policy sent without allowPause keeps the
// stored false rather than taking a create's true.
func TestAForkKeepsWhatTheEditLeavesOut(t *testing.T) {
	w := newForkWorld(t)
	underlined := json.RawMessage(`{"format":"semantic_v1","blocks":[{"type":"paragraph","content":[{"type":"text","text":"word","marks":["underline"]}]}]}`)
	audio := w.audio(t, w.a)
	in := choiceInput("Nghe và chọn", "word", "other")
	in.Options[0].Content = underlined
	in.MediaAssetID = &audio
	in.Audio = &domain.AudioPolicy{AllowSeek: true, AllowPause: ptr(false)}
	q := w.createWith(t, w.a, in)
	t1 := w.draftUsing(t, w.a, q.ID)
	_ = w.draftUsing(t, w.a, q.ID)
	w.grantTest(t, t1, w.b, "edit")

	omitting := func(prompt, first string) domain.Input {
		edit := in
		edit.Prompt = prompt
		edit.Options = []domain.OptionInput{{ID: &q.Options[0].ID, Text: first, IsCorrect: true}, {ID: &q.Options[1].ID, Text: "other"}}
		edit.Audio = &domain.AudioPolicy{AllowSeek: true}
		return edit
	}

	_, err := w.editInTest(w.b, false, t1, q.ID, omitting("B đổi chữ đã định dạng", "words"))
	var invalid *validation.Error
	if !errors.As(err, &invalid) || invalid.Fields[0].Field != "options[0].content" {
		t.Fatalf("a fork changing a formatted option's text without its content answered %v, want the options[0].content refusal", err)
	}
	if got := w.draftHolds(t, t1); !slices.Equal(got, []string{q.ID}) || w.bankSize(t, w.a) != 1 {
		t.Fatalf("the refused fork left T1 holding %v and A's bank %d questions, want Q and 1", got, w.bankSize(t, w.a))
	}

	edit := omitting("B sửa, không gửi định dạng", "word")
	edited, err := w.editInTest(w.b, false, t1, q.ID, edit)
	if err != nil || !edited.Forked {
		t.Fatalf("B's edit answered forked=%v, %v, want a fork", edited.Forked, err)
	}
	fork := edited.Question
	if len(fork.Options) != 2 || !sameJSON(t, fork.Options[0].Content, underlined) || fork.Options[1].Content != nil {
		t.Errorf("the fork's options are %+v, want the first keeping its formatting and the second plain", fork.Options)
	}
	if fork.Audio == nil || fork.Audio.AllowPause == nil || *fork.Audio.AllowPause {
		t.Errorf("the fork's audio policy is %+v, want allowPause kept false", fork.Audio)
	}
	if edit.Options[0].Content != nil || edit.Audio.AllowPause != nil {
		t.Error("the fork filled the caller's input in place")
	}
	if got, err := w.get(access.Scope{UserID: w.a}, q.ID); err != nil || got.Audio == nil || *got.Audio.AllowPause || !sameJSON(t, got.Options[0].Content, underlined) {
		t.Errorf("Q reads %+v (%v) after the fork, want it unchanged", got, err)
	}
}

// TestWhatElseUsesTheQuestionDecidesTheFork: each other use the rule counts,
// alone, forks the edit (an archived test, a draft for an edit in a set), and
// a deleted test does not.
func TestWhatElseUsesTheQuestionDecidesTheFork(t *testing.T) {
	t.Run("an archived test", func(t *testing.T) {
		w := newForkWorld(t)
		q := w.create(t, w.a, "Câu ở đề lưu trữ", nil)
		t1, t2 := w.draftUsing(t, w.a, q.ID), w.draftUsing(t, w.a, q.ID)
		if _, err := w.tx.Exec(context.Background(), `UPDATE app.tests SET status = 'archived' WHERE id = $1`, t2); err != nil {
			t.Fatal(err)
		}
		w.grantTest(t, t1, w.b, "edit")
		edited, err := w.editInTest(w.b, false, t1, q.ID, w.input("B sửa", nil))
		if err != nil || !edited.Forked {
			t.Fatalf("with an archived T2 using Q, B's edit answered forked=%v, %v, want a fork", edited.Forked, err)
		}
		if got := w.draftHolds(t, t2); !slices.Equal(got, []string{q.ID}) {
			t.Errorf("the archived T2 holds %v, want Q", got)
		}
	})

	t.Run("a deleted test", func(t *testing.T) {
		w := newForkWorld(t)
		q := w.create(t, w.a, "Câu ở đề đã xoá", nil)
		t1, t2 := w.draftUsing(t, w.a, q.ID), w.draftUsing(t, w.a, q.ID)
		if _, err := w.tx.Exec(context.Background(), `UPDATE app.tests SET deleted_at = now() WHERE id = $1`, t2); err != nil {
			t.Fatal(err)
		}
		w.grantTest(t, t1, w.b, "edit")
		edited, err := w.editInTest(w.b, false, t1, q.ID, w.input("B sửa", nil))
		if err != nil || edited.Forked || edited.Question.ID != q.ID {
			t.Fatalf("with only a deleted T2 using Q, B's edit answered forked=%v %s, %v, want Q in place", edited.Forked, edited.Question.ID, err)
		}
	})

	t.Run("a draft, for an edit in a set", func(t *testing.T) {
		w := newForkWorld(t)
		q := w.create(t, w.a, "Câu ở bộ và đề", nil)
		s2 := w.makeSet(t, w.a, "Bộ S2", q.ID)
		t1 := w.draftUsing(t, w.a, q.ID)
		w.grantSet(t, s2.ID, w.b, "edit")
		fork, err := w.svc.Commands.Update.Handle(context.Background(), command.Update{Request: request(w.b, false, q.ID, w.input("B sửa trong S2", nil)), SetID: s2.ID})
		if err != nil || fork.ID == q.ID {
			t.Fatalf("with T1 using Q, B's edit in S2 answered %s, %v, want a fork", fork.ID, err)
		}
		if got := w.setHolds(t, s2.ID); !slices.Equal(got, []string{fork.ID}) {
			t.Errorf("S2 holds %v, want Q′", got)
		}
		if got := w.draftHolds(t, t1); !slices.Equal(got, []string{q.ID}) {
			t.Errorf("T1 holds %v, want Q", got)
		}
	})
}

// TestAForkRepointsEveryRowOfTheContextTest: T2 uses Q only through a
// group-aware outline's unit, which alone forks B's edit in T1. T1 holds Q
// twice in its legacy outline, in two sections, and once as a unit: every one
// of those rows names Q′ afterwards, at its own section and ordinal, and the
// other question in T1 and T2's unit are untouched.
func TestAForkRepointsEveryRowOfTheContextTest(t *testing.T) {
	w := newForkWorld(t)
	q, other := w.create(t, w.a, "Câu ở hai phần", nil), w.create(t, w.a, "Câu khác", nil)
	t1 := w.id(t, `INSERT INTO app.tests (title, created_by, owner_id) VALUES ('T1', $1, $1) RETURNING id::text`, w.a)
	first, second := w.section(t, t1, 0), w.section(t, t1, 1)
	w.hold(t, "test_section_questions", first, 0, other.ID)
	w.hold(t, "test_section_questions", first, 1, q.ID)
	w.hold(t, "test_section_questions", second, 0, q.ID)
	w.hold(t, "test_section_units", second, 3, q.ID)
	t2 := w.id(t, `INSERT INTO app.tests (title, created_by, owner_id) VALUES ('T2', $1, $1) RETURNING id::text`, w.a)
	w.hold(t, "test_section_units", w.section(t, t2, 0), 0, q.ID)
	w.grantTest(t, t1, w.b, "edit")

	edited, err := w.editInTest(w.b, false, t1, q.ID, w.input("B sửa ở mọi chỗ của T1", nil))
	if err != nil || !edited.Forked {
		t.Fatalf("with T2's unit using Q, B's edit answered forked=%v, %v, want a fork", edited.Forked, err)
	}
	fork := edited.Question.ID
	want := []string{
		"questions 0/0 " + other.ID, "questions 0/1 " + fork, "questions 1/0 " + fork,
		"units 1/3 " + fork,
	}
	if got := w.outline(t, t1); !slices.Equal(got, want) {
		t.Errorf("T1's outline is %v, want %v", got, want)
	}
	if got := w.outline(t, t2); !slices.Equal(got, []string{"units 0/0 " + q.ID}) {
		t.Errorf("T2's outline is %v, want its unit still naming Q", got)
	}
}

// TestAForkCopiesAMatchingQuestionsItemsAndAnEssaysRubric: the edit, sent as
// a client that loaded Q sends it, with Q's option, item and criterion ids,
// makes a copy whose children are its own rows: fresh ids, the same texts,
// and each item keyed to the copy's option at the same place. Q keeps its own.
func TestAForkCopiesAMatchingQuestionsItemsAndAnEssaysRubric(t *testing.T) {
	forkOf := func(t *testing.T, w *forkWorld, in domain.Input, edit func(domain.Question) domain.Input) (domain.Question, domain.Question) {
		t.Helper()
		q := w.createWith(t, w.a, in)
		t1 := w.draftUsing(t, w.a, q.ID)
		_ = w.draftUsing(t, w.a, q.ID)
		w.grantTest(t, t1, w.b, "edit")
		edited, err := w.editInTest(w.b, false, t1, q.ID, edit(q))
		if err != nil || !edited.Forked {
			t.Fatalf("B's edit answered forked=%v, %v, want a fork", edited.Forked, err)
		}
		return q, edited.Question
	}

	t.Run("matching", func(t *testing.T) {
		w := newForkWorld(t)
		q, fork := forkOf(t, w, matchingInput(), func(q domain.Question) domain.Input {
			edit := matchingInput()
			edit.Prompt = "B nối lại"
			for i := range edit.Options {
				edit.Options[i].ID = &q.Options[i].ID
			}
			for i := range edit.MatchItems {
				edit.MatchItems[i].ID = &q.MatchItems[i].ID
			}
			return edit
		})
		if len(fork.Options) != 3 || len(fork.MatchItems) != 2 {
			t.Fatalf("the fork holds %d options and %d items, want 3 and 2", len(fork.Options), len(fork.MatchItems))
		}
		for i, option := range fork.Options {
			if option.ID == q.Options[i].ID || option.Text != q.Options[i].Text {
				t.Errorf("the fork's option %d is %s %q, want a new row with %q", i, option.ID, option.Text, q.Options[i].Text)
			}
		}
		for i, item := range fork.MatchItems {
			want := q.MatchItems[i]
			if item.ID == want.ID || item.Text != want.Text || item.CorrectOption != want.CorrectOption ||
				item.CorrectOptionID != fork.Options[item.CorrectOption].ID {
				t.Errorf("the fork's item %d is %+v, want a new row like %+v keyed to the fork's option %d", i, item, want, want.CorrectOption)
			}
		}
		if fork.MatchAllowReuse == nil || *fork.MatchAllowReuse {
			t.Errorf("the fork's reuse is %v, want false", fork.MatchAllowReuse)
		}
		after, err := w.get(access.Scope{UserID: w.a}, q.ID)
		if err != nil || len(after.MatchItems) != 2 || after.MatchItems[0].ID != q.MatchItems[0].ID || after.MatchItems[0].CorrectOptionID != q.Options[2].ID {
			t.Errorf("Q's items are %+v (%v) after the fork, want its own, unchanged", after.MatchItems, err)
		}
	})

	t.Run("essay", func(t *testing.T) {
		w := newForkWorld(t)
		in := domain.Input{Tags: []string{}, Type: domain.Essay, Prompt: "Viết về thành phố của bạn.", Points: "9",
			Rubric: []domain.RubricCriterionInput{{Label: "Task response"}, {Label: "Coherence"}}}
		q, fork := forkOf(t, w, in, func(q domain.Question) domain.Input {
			edit := in
			edit.Prompt = "B sửa đề viết"
			edit.Rubric = []domain.RubricCriterionInput{{ID: &q.Rubric[1].ID, Label: "Coherence"}, {ID: &q.Rubric[0].ID, Label: "Task achievement"}}
			return edit
		})
		if len(fork.Rubric) != 2 || fork.Rubric[0].Label != "Coherence" || fork.Rubric[1].Label != "Task achievement" {
			t.Fatalf("the fork's rubric is %+v, want the edit's two criteria in order", fork.Rubric)
		}
		for _, criterion := range fork.Rubric {
			if criterion.ID == q.Rubric[0].ID || criterion.ID == q.Rubric[1].ID {
				t.Errorf("the fork's criterion %q took Q's row %s", criterion.Label, criterion.ID)
			}
		}
		after, err := w.get(access.Scope{UserID: w.a}, q.ID)
		if err != nil || len(after.Rubric) != 2 || after.Rubric[0].ID != q.Rubric[0].ID || after.Rubric[0].Label != "Task response" {
			t.Errorf("Q's rubric is %+v (%v) after the fork, want its own, unchanged", after.Rubric, err)
		}
	})
}
