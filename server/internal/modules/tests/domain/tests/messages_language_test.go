package domain_test

import (
	"errors"
	"strings"
	"testing"

	"quizzivy/internal/modules/tests/domain"
)

// An outline that breaks every rule UpdateInput.Validate words, one field each
// where the rules allow it.
func refusedOutlines() []domain.UpdateInput {
	blank, unknown, published := "  ", domain.Status("gone"), domain.Published
	group := domain.SectionUnit{Kind: "group", ID: groupID(1)}
	question := domain.SectionUnit{Kind: "question", ID: groupID(2)}
	return []domain.UpdateInput{
		{Title: &blank, Status: &unknown},
		{Status: &published},
		{SetSections: true, Sections: []domain.SectionInput{
			{ID: "a", Title: " ", QuestionIDs: []string{groupID(2), groupID(2)}, SetUnits: true},
			{ID: "A", Title: "Part"},
		}},
		{GroupOutline: true, Sections: []domain.SectionInput{
			{ID: "a", Title: "Part"},
			{ID: "b", Title: "Part", SetUnits: true, Units: []domain.SectionUnit{group, group, {Kind: "material", ID: groupID(3)}, question}},
			{ID: "c", Title: "Part", SetUnits: true, Units: make([]domain.SectionUnit, 32769)},
		}},
	}
}

func TestEveryOutlineRefusalIsWordedInVietnameseAndEnglish(t *testing.T) {
	sentences := map[string]bool{}
	for i, in := range refusedOutlines() {
		var invalid *domain.ValidationError
		if !errors.As(in.Validate(), &invalid) {
			t.Fatalf("outline %d was accepted", i)
		}
		for _, field := range invalid.Fields {
			if field.English == "" || field.English == field.Message {
				t.Errorf("%s is refused with %q and no English sentence of its own (%q)", field.Field, field.Message, field.English)
			}
			sentences[field.Message] = true
		}
	}
	if len(sentences) != 13 {
		t.Errorf("the outlines draw %d different refusals, want all 13 the outline rules word", len(sentences))
	}
}

func TestEveryPublishViolationOfTheTestIsWordedInVietnameseAndEnglish(t *testing.T) {
	huge := domain.DraftQuestion{SourceID: groupID(31), Type: "short_answer", Prompt: "Hà Nội?", Points: "999999.99"}
	another := huge
	another.SourceID = groupID(32)
	broken := groupFixture()
	broken.Questions = nil
	for name, c := range map[string]struct {
		draft domain.DraftContent
		rule  domain.Rule
	}{
		"no sections": {domain.DraftContent{}, domain.SectionNotEmpty},
		"an empty section": {domain.DraftContent{Sections: []domain.DraftSection{
			{ID: groupID(30), Title: "Reading"},
		}}, domain.SectionNotEmpty},
		"a total past the limit": {domain.DraftContent{Sections: []domain.DraftSection{
			{ID: groupID(30), Title: "Reading", Questions: []domain.DraftQuestion{huge, another}},
		}}, domain.TotalPointsValid},
		"a group that is not complete": {domain.DraftContent{Sections: []domain.DraftSection{
			{ID: groupID(30), Title: "Reading", Questions: []domain.DraftQuestion{huge}, Groups: []domain.GroupBundle{broken}},
		}}, domain.GroupValid},
	} {
		t.Run(name, func(t *testing.T) {
			var invalid *domain.PublishValidationError
			if !errors.As(domain.Publishing.Validate(c.draft), &invalid) {
				t.Fatal("the draft was accepted")
			}
			found := false
			for _, violation := range invalid.Violations {
				if violation.English == "" || violation.English == violation.Message {
					t.Errorf("%s is worded %q with no English sentence of its own (%q)", violation.Rule, violation.Message, violation.English)
				}
				found = found || violation.Rule == c.rule
			}
			if !found {
				t.Errorf("no %s among %+v", c.rule, invalid.Violations)
			}
		})
	}
}

func TestAnEmptySectionIsNamedInBothLanguages(t *testing.T) {
	var invalid *domain.PublishValidationError
	draft := domain.DraftContent{Sections: []domain.DraftSection{{ID: groupID(30), Title: "Đọc hiểu"}}}
	if !errors.As(domain.Publishing.Validate(draft), &invalid) || len(invalid.Violations) != 1 {
		t.Fatalf("want the one violation of an empty section, got %+v", invalid)
	}
	got := invalid.Violations[0]
	if !strings.Contains(got.Message, `"Đọc hiểu"`) || !strings.Contains(got.English, `"Đọc hiểu"`) {
		t.Errorf("the section is not named in both sentences: %q and %q", got.Message, got.English)
	}
}
