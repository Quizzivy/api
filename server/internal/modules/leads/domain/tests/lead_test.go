package domain_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"quizzivy/internal/modules/leads/domain"
	"quizzivy/internal/shared/validation"
)

func form() domain.Form {
	return domain.Form{
		FullName: "Trần Thị Mai", Phone: "0903 123 456", Interests: []string{"ielts", "kids"},
		CourseFor: "child", CallTime: "evening", Locale: "vi", PrivacyNoticeVersion: "2026-11",
	}
}

func refusedFields(t *testing.T, err error) []string {
	t.Helper()
	var invalid *validation.Error
	if !errors.As(err, &invalid) {
		t.Fatalf("error = %v, want a *validation.Error", err)
	}
	var fields []string
	for _, f := range invalid.Fields {
		fields = append(fields, f.Field)
	}
	return fields
}

// TestAFormIsStoredWithItsNameTidiedAndItsNumberAsDigits fails when Normalised
// stops composing the name to NFC, trimming it or folding runs of white space,
// keeps anything but the digits of the number, or alters a choice the form
// made. validation's own test covers CleanName's length; none covers a form.
func TestAFormIsStoredWithItsNameTidiedAndItsNumberAsDigits(t *testing.T) {
	f := form()
	f.FullName = "  Tra\u0302\u0300n   Thi\u0323\tMai \n"
	f.Phone = "+84 (90) 312-34.56"
	got, err := f.Normalised()
	if err != nil {
		t.Fatal(err)
	}
	if got.FullName != "Trần Thị Mai" {
		t.Errorf("name = %q, want it composed, trimmed and single-spaced", got.FullName)
	}
	if got.PhoneDigits != "84903123456" {
		t.Errorf("digits = %q, want 84903123456", got.PhoneDigits)
	}
	if !slices.Equal(got.Interests, []string{"ielts", "kids"}) || got.CourseFor != "child" || got.CallTime != "evening" ||
		got.Locale != "vi" || got.PrivacyNoticeVersion != "2026-11" || got.Honeypot {
		t.Errorf("submission = %+v, want the form's choices unchanged and no honeypot", got)
	}
	f.Interests = nil
	if got, err := f.Normalised(); err != nil || got.Interests == nil {
		t.Errorf("a form with no interest: %+v, %v; want an empty list, which the column takes", got, err)
	}
}

// TestANumberIsKeptFromNineToFifteenDigitsHoweverItIsWritten fails when the
// bounds move, or are counted on the characters typed rather than on the
// digits: the schema's pattern admits 9 to 20 characters, so a number of 8
// digits with punctuation passes it and only this rule refuses it.
func TestANumberIsKeptFromNineToFifteenDigitsHoweverItIsWritten(t *testing.T) {
	for phone, want := range map[string]bool{
		"12345678":               false,
		"1234 5678":              false,
		"(+)1.2-3 4 5 6 7 8":     false,
		"123456789":              true,
		"0903.123.456":           true,
		"123456789012345":        true,
		"+1 234 567 890 123 456": false,
		"1234567890123456":       false,
		"+++++++++":              false,
	} {
		f := form()
		f.Phone = phone
		_, err := f.Normalised()
		if (err == nil) != want {
			t.Errorf("phone %q: err = %v, want accepted = %v", phone, err, want)
			continue
		}
		if err != nil && !slices.Equal(refusedFields(t, err), []string{domain.FieldPhone}) {
			t.Errorf("phone %q refused %v, want only phone", phone, refusedFields(t, err))
		}
	}
}

// TestANameThatCannotBeStoredIsRefusedByItsFieldWithoutRepeatingIt fails when
// an empty or unreadable name is accepted, is refused under another field's
// name, loses its English sentence, or is echoed back in the message; and
// when a form wrong in two fields names only one.
func TestANameThatCannotBeStoredIsRefusedByItsFieldWithoutRepeatingIt(t *testing.T) {
	for name, raw := range map[string]string{
		"only spaces":           " \t  ",
		"a zero-width space":    "Mai\u200bLan",
		"a control character":   "Mai\u0007",
		"a right-to-left mark":  "Mai\u200f",
		"only a combining mark": "\u0301",
		"121 characters":        strings.Repeat("ê", 121),
	} {
		f := form()
		f.FullName = raw
		_, err := f.Normalised()
		if err == nil {
			t.Errorf("%s: the name was accepted", name)
			continue
		}
		if got := refusedFields(t, err); !slices.Equal(got, []string{domain.FieldFullName}) {
			t.Errorf("%s: refused %v, want only fullName", name, got)
		}
		var invalid *validation.Error
		errors.As(err, &invalid)
		for _, field := range invalid.Fields {
			if strings.TrimSpace(raw) != "" && (strings.Contains(field.Message, raw) || strings.Contains(field.English, raw)) {
				t.Errorf("%s: the message repeats the name: %q / %q", name, field.Message, field.English)
			}
			if field.English == "" {
				t.Errorf("%s: the refusal has no English sentence", name)
			}
		}
	}
	both := form()
	both.FullName, both.Phone = "", "12"
	if _, err := both.Normalised(); err == nil || !slices.Equal(refusedFields(t, err), []string{domain.FieldFullName, domain.FieldPhone}) {
		t.Errorf("a form wrong twice: %v, want both fields named at once", err)
	}
}

// TestAFilledUnseenFieldMarksTheSubmissionAndAnEmptyOneDoesNot fails when a
// filled website no longer marks the submission as a honeypot, or an absent
// one does, which would drop every real request.
func TestAFilledUnseenFieldMarksTheSubmissionAndAnEmptyOneDoesNot(t *testing.T) {
	f := form()
	if got, _ := f.Normalised(); got.Honeypot {
		t.Error("a form that left website out is marked")
	}
	f.Website = "https://spam.example"
	if got, _ := f.Normalised(); !got.Honeypot {
		t.Error("a form that filled website is not marked")
	}
}

// TestTheVerdictDropsAHoneypotAndTheThirdRequestOfANumber fails when the cap
// is not two requests in 24 hours, when the request at the cap is stored, and
// when a honeypot is judged by the count instead of first.
func TestTheVerdictDropsAHoneypotAndTheThirdRequestOfANumber(t *testing.T) {
	plain, trapped := domain.Submission{}, domain.Submission{Honeypot: true}
	for recent, want := range map[int]domain.Drop{0: domain.Stored, 1: domain.Stored, 2: domain.DropRepeat, 3: domain.DropRepeat, 40: domain.DropRepeat} {
		if got := plain.Verdict(recent); got != want {
			t.Errorf("%d requests already stored: verdict %q, want %q", recent, got, want)
		}
	}
	for _, recent := range []int{0, 1, 2, 9} {
		if got := trapped.Verdict(recent); got != domain.DropHoneypot {
			t.Errorf("a honeypot with %d stored: verdict %q, want honeypot whatever the count", recent, got)
		}
	}
	if domain.RepeatCap != 2 || domain.RepeatWindow.Hours() != 24 {
		t.Errorf("the cap is %d in %s, want 2 in 24 hours", domain.RepeatCap, domain.RepeatWindow)
	}
}

// TestTheGivenNameIsTheLastWordInVietnameseAndTheFirstInEnglish fails when a
// notification would greet a Vietnamese sender by the family name, or an
// English one by the last word; the word it returns is all of the name an
// email may carry.
func TestTheGivenNameIsTheLastWordInVietnameseAndTheFirstInEnglish(t *testing.T) {
	for _, c := range []struct{ name, locale, want string }{
		{"Trần Thị Mai", "vi", "Mai"},
		{"Trần Thị Mai", "en", "Trần"},
		{"Mary Jane Watson", "en", "Mary"},
		{"Mary Jane Watson", "vi", "Watson"},
		{"Mai", "vi", "Mai"},
		{"Mai", "en", "Mai"},
		{"", "vi", ""},
	} {
		if got := domain.GivenName(c.name, c.locale); got != c.want {
			t.Errorf("GivenName(%q, %s) = %q, want %q", c.name, c.locale, got, c.want)
		}
	}
}

// TestAChangeKeepsANoteItDoesNotNameAndTidiesOneItDoes fails when a change
// that names no note carries one to the store, when a note is kept untrimmed
// or decomposed, when a blank one is not the empty note that removes it, and
// when the bound of 1000 characters moves.
func TestAChangeKeepsANoteItDoesNotNameAndTidiesOneItDoes(t *testing.T) {
	contacted := domain.StatusContacted
	got, err := domain.Change{Status: &contacted, Note: "ignored"}.Normalised()
	if err != nil || got.NoteSet || got.Note != "" || got.Status == nil || *got.Status != domain.StatusContacted {
		t.Errorf("a change without a note = %+v, %v", got, err)
	}
	got, err = domain.Change{NoteSet: true, Note: "  Go\u0323i la\u0323i \r\n sau 18h  "}.Normalised()
	if err != nil || got.Note != "Gọi lại \n sau 18h" || !got.NoteSet {
		t.Errorf("a note = %q, %v; want it composed and trimmed", got.Note, err)
	}
	got, err = domain.Change{NoteSet: true, Note: " \n "}.Normalised()
	if err != nil || !got.NoteSet || got.Note != "" {
		t.Errorf("a blank note = %+v, %v; want a set, empty note, which removes it", got, err)
	}
	if _, err := (domain.Change{NoteSet: true, Note: strings.Repeat("ă", 1000)}).Normalised(); err != nil {
		t.Errorf("a note of 1000 characters was refused: %v", err)
	}
	_, err = domain.Change{NoteSet: true, Note: strings.Repeat("ă", 1001)}.Normalised()
	if err == nil || !slices.Equal(refusedFields(t, err), []string{domain.FieldNote}) {
		t.Errorf("a note of 1001 characters: %v, want note refused", err)
	}
}

// TestOnlyTheThreeStatusesAreValid fails when a status is added to or dropped
// from the three the column's CHECK admits.
func TestOnlyTheThreeStatusesAreValid(t *testing.T) {
	for _, s := range []domain.Status{domain.StatusNew, domain.StatusContacted, domain.StatusClosed} {
		if !s.Valid() {
			t.Errorf("%q is not valid", s)
		}
	}
	for _, s := range []domain.Status{"", "New", "archived"} {
		if s.Valid() {
			t.Errorf("%q is valid", s)
		}
	}
}
