package httpapi_test

import (
	"testing"

	"quizzivy/internal/platform/httpapi"
)

func TestAQuestionsWordLimitIsAnEssaysEssayMaxWordsAndEveryOtherTypesMaxWords(t *testing.T) {
	limit := 250
	if short, essay := httpapi.WordLimits(true, &limit); short != nil || essay == nil || *essay != 250 {
		t.Errorf("an essay's limit reads maxWords %v, essayMaxWords %v", short, essay)
	}
	if short, essay := httpapi.WordLimits(false, &limit); essay != nil || short == nil || *short != 250 {
		t.Errorf("a short answer's limit reads maxWords %v, essayMaxWords %v", short, essay)
	}
	three := 3
	for _, c := range []struct {
		name            string
		essay           bool
		maxWords, other *int
		want            *int
		misplaced       string
	}{
		{"an essay's essayMaxWords", true, nil, &limit, &limit, ""},
		{"maxWords on an essay", true, &three, nil, nil, "maxWords"},
		{"a short answer's maxWords", false, &three, nil, &three, ""},
		{"essayMaxWords on a short answer", false, nil, &limit, nil, "essayMaxWords"},
	} {
		got, misplaced := httpapi.WordLimitIn(c.essay, c.maxWords, c.other)
		if got != c.want || misplaced != c.misplaced {
			t.Errorf("%s: read %v, misplaced %q; want %v, %q", c.name, got, misplaced, c.want, c.misplaced)
		}
	}
}
