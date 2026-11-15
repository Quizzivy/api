package domain

import "quizzivy/internal/shared/validation"

// FullNameFault says why CleanFullName refused a name.
type FullNameFault = validation.NameFault

// The faults, in the order they are looked for.
const (
	FullNameOK      = validation.NameOK
	FullNameEmpty   = validation.NameEmpty
	FullNameTooLong = validation.NameTooLong
	FullNameHidden  = validation.NameHidden
	FullNameNoGlyph = validation.NameNoGlyph
)

// CleanFullName returns raw as the full name of an account: in Unicode NFC,
// with each run of white space one space and none at either end. It also says
// why the name cannot be kept: it is empty, longer than MaxFullNameLength
// characters, holds a character a reader cannot see (the first is returned as
// hidden), or holds no letter, digit, punctuation mark or symbol. The one rule
// serves a form and a file of accounts alike, so the two admit the same names.
func CleanFullName(raw string) (name string, fault FullNameFault, hidden rune) {
	return validation.CleanName(raw, MaxFullNameLength)
}
