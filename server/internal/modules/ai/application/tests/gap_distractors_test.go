package application_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"quizzivy/internal/modules/ai/application/ports"
	"quizzivy/internal/modules/ai/application/query"
	"quizzivy/internal/modules/ai/domain"
	"quizzivy/internal/modules/ai/domain/prompts"
)

const gapOperation = "gap_distractors"

func parksGap() query.GapDistractors {
	return query.GapDistractors{Actor: teacher, Before: "Parks give people a place to ", After: ".", Answers: []string{"relax", "rest"}, Options: []string{"work"}}
}

func TestWrongOptionsForAGapAreAskedFromItsSentenceItsAnswersAndItsOptionsAndNothingStoredIsRead(t *testing.T) {
	o := newOps(t, true)
	o.reads.err = domain.ErrNotFound

	got, err := o.app.Queries.GapDistractors.Handle(context.Background(), parksGap())
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Items) != 1 || got.Items[0].Text != "Libraries" || got.Usage.Left != left {
		t.Errorf("suggested %+v with usage %+v, want the assistant's one suggestion and what is left", got.Items, got.Usage)
	}
	if len(o.reads.calls) != 0 {
		t.Errorf("read %q, want nothing stored read for a gap the caller sends whole", o.reads.called())
	}
	sent := o.completer.text()
	for _, want := range []string{`"""Parks give people a place to ____."""`, "Correct answer: relax", "Also accepted: rest", "Existing wrong options: work", "Write why in English"} {
		if !strings.Contains(sent, want) {
			t.Errorf("the request to the assistant lacks %q:\n%s", want, sent)
		}
	}
}

func TestASuggestionForAGapThatRepeatsAnAnswerOrAnOptionIsDropped(t *testing.T) {
	o := newOps(t, true)
	o.completer.object = func(ports.Request) string {
		return `{"distractors":[
			{"text":"relax","why":"The word itself."},
			{"text":" REST ","why":"Also accepted."},
			{"text":"Work","why":"Already an option."},
			{"text":"play","why":"Another free-time verb."},
			{"text":"Play","why":"Said twice."},
			{"text":"sleep","why":"Close in meaning."}]}`
	}

	got, err := o.app.Queries.GapDistractors.Handle(context.Background(), parksGap())
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Items) != 2 || got.Items[0].Text != "play" || got.Items[1].Text != "sleep" {
		t.Errorf("suggested %+v, want only play and sleep", got.Items)
	}
}

func TestAnAnswerForAGapWithNothingNewIsUnavailableAndCostsNothing(t *testing.T) {
	o := newOps(t, true)
	o.completer.object = func(ports.Request) string {
		return `{"distractors":[{"text":"relax","why":"The word itself."},{"text":"work","why":"Already an option."}]}`
	}

	_, err := o.app.Queries.GapDistractors.Handle(context.Background(), parksGap())
	if !errors.Is(err, domain.ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable for an answer with no usable suggestion", err)
	}
}

func TestAGapWhoseFirstAnswerHoldsNoWordHasNoKeyAndCostsNothing(t *testing.T) {
	for name, answers := range map[string][]string{"none": nil, "blank": {"  ", "rest"}} {
		t.Run(name, func(t *testing.T) {
			o := newOps(t, true)
			q := parksGap()
			q.Answers = answers
			if _, err := o.app.Queries.GapDistractors.Handle(context.Background(), q); !errors.Is(err, domain.ErrNoKey) {
				t.Fatalf("err = %v, want ErrNoKey", err)
			}
			if len(o.repo.reserved) != 0 || len(o.completer.requests) != 0 {
				t.Errorf("reserved %d, called %d; a gap with no word costs nothing", len(o.repo.reserved), len(o.completer.requests))
			}
		})
	}
}

func TestAGapsPromptKeepsItsPartsToWhatTheContractAllowsAndNamesNoOptionWhereThereIsNone(t *testing.T) {
	long := strings.Repeat("word ", 600)
	many := make([]string, 40)
	for i := range many {
		many[i] = strings.Repeat("x", i+1)
	}
	prompt, err := prompts.GapDistractors(prompts.GapDistractorsInput{Before: long, After: long, Answers: many, Language: domain.Vietnamese})
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(prompt.User, "word"); n > 500 {
		t.Errorf("the sentence carries %d words, want it cut", n)
	}
	if n := strings.Count(prompt.User, " | "); n != prompts.MaxGapAnswers-2 {
		t.Errorf("%d separators among the other accepted answers, want %d answers named after the first", n, prompts.MaxGapAnswers-1)
	}
	for _, want := range []string{"Existing wrong options: none", "Write why in Vietnamese"} {
		if !strings.Contains(prompt.User, want) {
			t.Errorf("the prompt lacks %q", want)
		}
	}
	if prompt.Feature != domain.FeatureDistractors {
		t.Errorf("metered as %s, want the wrong options' feature", prompt.Feature)
	}
}
