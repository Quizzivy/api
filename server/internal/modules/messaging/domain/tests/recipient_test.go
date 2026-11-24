package domain_test

import (
	"encoding/base64"
	"errors"
	"testing"

	"github.com/google/uuid"

	"quizzivy/internal/modules/messaging/domain"
)

// TestThePickersFiltersAreTheContracts400s: one students' filter at most,
// none for colleagues, a known audience; the limit clamped to 1..50 with 30
// when unset.
func TestThePickersFiltersAreTheContracts400s(t *testing.T) {
	students, colleagues := domain.AudienceStudents, domain.AudienceColleagues
	class := uuid.NewString()
	for name, c := range map[string]struct {
		f   domain.RecipientFilter
		err error
	}{
		"students, no filter":          {domain.RecipientFilter{Audience: students}, nil},
		"students in a class":          {domain.RecipientFilter{Audience: students, ClassID: class}, nil},
		"students of mine":             {domain.RecipientFilter{Audience: students, Mine: true}, nil},
		"students in no class":         {domain.RecipientFilter{Audience: students, InNoClass: true}, nil},
		"colleagues, no filter":        {domain.RecipientFilter{Audience: colleagues}, nil},
		"a class with mine":            {domain.RecipientFilter{Audience: students, ClassID: class, Mine: true}, domain.ErrInvalid},
		"a class in no class":          {domain.RecipientFilter{Audience: students, ClassID: class, InNoClass: true}, domain.ErrInvalid},
		"mine in no class":             {domain.RecipientFilter{Audience: students, Mine: true, InNoClass: true}, domain.ErrInvalid},
		"colleagues in a class":        {domain.RecipientFilter{Audience: colleagues, ClassID: class}, domain.ErrInvalid},
		"colleagues of mine":           {domain.RecipientFilter{Audience: colleagues, Mine: true}, domain.ErrInvalid},
		"colleagues in no class":       {domain.RecipientFilter{Audience: colleagues, InNoClass: true}, domain.ErrInvalid},
		"an audience the picker lacks": {domain.RecipientFilter{Audience: "parents"}, domain.ErrInvalid},
	} {
		if _, err := c.f.Check(); !errors.Is(err, c.err) {
			t.Errorf("%s: %v, want %v", name, err, c.err)
		}
	}
	for limit, want := range map[int]int{0: 30, -3: 30, 1: 1, 50: 50, 51: 50} {
		if f, _ := (domain.RecipientFilter{Audience: students, Limit: limit}).Check(); f.Limit != want {
			t.Errorf("limit %d = %d, want %d", limit, f.Limit, want)
		}
	}
}

// TestAPickerCursorNamesItsRowAndNothingElse: a cursor round-trips the row
// id it was made from, stays well under the contract's 512 characters, and
// anything the picker did not make is ErrInvalid.
func TestAPickerCursorNamesItsRowAndNothingElse(t *testing.T) {
	id := uuid.NewString()
	cursor := domain.RecipientCursor(id)
	if got, err := domain.ParseRecipientCursor(cursor); err != nil || got != id {
		t.Errorf("the cursor of %s parses as %q, %v", id, got, err)
	}
	if len(cursor) > 512 {
		t.Errorf("a cursor of %d characters", len(cursor))
	}
	for name, raw := range map[string]string{
		"not base64":              "%%%",
		"padded base64":           base64.URLEncoding.EncodeToString([]byte("recipients:" + id)),
		"another list's prefix":   base64.RawURLEncoding.EncodeToString([]byte("threads:" + id)),
		"a bare id":               base64.RawURLEncoding.EncodeToString([]byte(id)),
		"no uuid":                 base64.RawURLEncoding.EncodeToString([]byte("recipients:someone")),
		"an upper-case uuid":      base64.RawURLEncoding.EncodeToString([]byte("recipients:" + "0195A1B2-0000-7000-8000-000000000000")),
		"a uuid with extra bytes": base64.RawURLEncoding.EncodeToString([]byte("recipients:" + id + "x")),
		"empty":                   "",
	} {
		if _, err := domain.ParseRecipientCursor(raw); !errors.Is(err, domain.ErrInvalid) {
			t.Errorf("%s: %v, want ErrInvalid", name, err)
		}
	}
}
