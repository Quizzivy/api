package domain_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"quizzivy/internal/modules/questions/domain"
)

func setName(name string) domain.SetFields { return domain.SetFields{Name: &name} }

func TestASetNameIsTrimmedAndOneToAHundredAndTwentyCharacters(t *testing.T) {
	for label, c := range map[string]struct {
		name string
		want string
		ok   bool
	}{
		"a name":                       {"Ngữ pháp", "Ngữ pháp", true},
		"spaces around it":             {"  Ngữ pháp \n", "Ngữ pháp", true},
		"decomposed Vietnamese":        {"Ngày", "Ngày", true},
		"a hundred and twenty letters": {strings.Repeat("ệ", domain.MaxSetName), strings.Repeat("ệ", domain.MaxSetName), true},
		"one letter more":              {strings.Repeat("ệ", domain.MaxSetName+1), "", false},
		"nothing":                      {"", "", false},
		"only spaces":                  {"   ", "", false},
	} {
		got, err := setName(c.name).Clean()
		var invalid *domain.ValidationError
		switch {
		case c.ok && (err != nil || *got.Name != c.want):
			t.Errorf("%s: %v, %v; want %q", label, got.Name, err, c.want)
		case !c.ok && (!errors.As(err, &invalid) || invalid.Fields[0].Field != "name"):
			t.Errorf("%s: err = %v, want a refusal of name", label, err)
		}
	}
}

func TestASetDescriptionIsTrimmedEmptyIsNoneAndAtMostFiveHundred(t *testing.T) {
	describe := func(d string) (domain.SetFields, error) {
		return domain.SetFields{Description: &d, DescriptionSet: true}.Clean()
	}
	if got, err := describe("  Cho lớp tối "); err != nil || got.Description == nil || *got.Description != "Cho lớp tối" || !got.DescriptionSet {
		t.Errorf("a description reads %v, %v", got.Description, err)
	}
	if got, err := describe("   "); err != nil || got.Description != nil || !got.DescriptionSet {
		t.Errorf("a blank description reads %v, %v; want none, set", got.Description, err)
	}
	if _, err := describe(strings.Repeat("ệ", domain.MaxSetDescription)); err != nil {
		t.Errorf("a description at the limit: %v", err)
	}
	var invalid *domain.ValidationError
	if _, err := describe(strings.Repeat("ệ", domain.MaxSetDescription+1)); !errors.As(err, &invalid) || invalid.Fields[0].Field != "description" {
		t.Errorf("an overlong description: %v", err)
	}
	if got, err := (domain.SetFields{}).Clean(); err != nil || got.Name != nil || got.Description != nil || got.DescriptionSet {
		t.Errorf("no fields read %+v, %v; want nothing changed", got, err)
	}
}

func ids(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = uuid.NewString()
	}
	return out
}

func TestASetWriteNamesOneToFiveHundredDistinctQuestions(t *testing.T) {
	repeated := ids(2)
	repeated[1] = repeated[0]
	for label, c := range map[string]struct {
		ids []string
		ok  bool
	}{
		"one":                {ids(1), true},
		"five hundred":       {ids(domain.MaxSetQuestions), true},
		"none":               {nil, false},
		"five hundred one":   {ids(domain.MaxSetQuestions + 1), false},
		"one question twice": {repeated, false},
	} {
		err := domain.SetQuestionIDs(c.ids)
		var invalid *domain.ValidationError
		switch {
		case c.ok && err != nil:
			t.Errorf("%s: refused with %v", label, err)
		case !c.ok && (!errors.As(err, &invalid) || invalid.Fields[0].Field != "questionIds"):
			t.Errorf("%s: err = %v, want a refusal of questionIds", label, err)
		}
	}
}

func TestASetHoldsAtMostFiveHundredQuestionsWhateverAnAddBrings(t *testing.T) {
	for _, c := range []struct {
		held, adding int
		full         bool
	}{
		{0, 500, false}, {499, 1, false}, {500, 0, false}, {500, 1, true}, {1, 500, true},
	} {
		if err := domain.SetCapacity(c.held, c.adding); errors.Is(err, domain.ErrSetFull) != c.full || !c.full && err != nil {
			t.Errorf("%d held and %d added: %v, want full %v", c.held, c.adding, err, c.full)
		}
	}
}

func TestOnlyTheOwnerAddsAndDeletesAndAUseGrantChangesNothing(t *testing.T) {
	for _, c := range []struct {
		access domain.Access
		action domain.SetAction
		want   error
	}{
		{domain.AccessOwner, domain.SetRead, nil},
		{domain.AccessOwner, domain.SetChange, nil},
		{domain.AccessOwner, domain.SetAdd, nil},
		{domain.AccessOwner, domain.SetDelete, nil},
		{domain.AccessEdit, domain.SetRead, nil},
		{domain.AccessEdit, domain.SetChange, nil},
		{domain.AccessEdit, domain.SetAdd, domain.ErrSetOwnerOnly},
		{domain.AccessEdit, domain.SetDelete, domain.ErrSetOwnerOnly},
		{domain.AccessUse, domain.SetRead, nil},
		{domain.AccessUse, domain.SetChange, domain.ErrSetReadOnly},
		{domain.AccessUse, domain.SetAdd, domain.ErrSetOwnerOnly},
		{domain.AccessUse, domain.SetDelete, domain.ErrSetOwnerOnly},
	} {
		if got := c.access.Allows(c.action); !errors.Is(got, c.want) || (c.want == nil) != (got == nil) {
			t.Errorf("%s doing %d: %v, want %v", c.access, c.action, got, c.want)
		}
	}
}

func TestAMissingQuestionsRefusalIsANotFound(t *testing.T) {
	err := error(&domain.QuestionsNotFoundError{IDs: []string{"a"}})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Error("a set's missing questions are not ErrNotFound")
	}
}
