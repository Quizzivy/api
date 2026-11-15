package validation

import (
	"strings"
	"unicode/utf8"
)

// NameFault says why CleanName refused a name.
type NameFault int

// The faults, in the order they are looked for.
const (
	NameOK NameFault = iota
	NameEmpty
	NameTooLong
	NameHidden
	NameNoGlyph
)

// CleanName returns raw as a person's name: in Unicode NFC, with each run of
// white space one space and none at either end. It also says why the name
// cannot be kept: it is empty, longer than limit characters, holds a character
// a reader cannot see (the first is returned as hidden), or holds no letter,
// digit, punctuation mark or symbol. An account's name and the name typed into
// the consultation form go through it, so both admit the same names.
func CleanName(raw string, limit int) (name string, fault NameFault, hidden rune) {
	name = Tidy(strings.Join(strings.Fields(raw), " "))
	switch {
	case name == "":
		return name, NameEmpty, 0
	case utf8.RuneCountInString(name) > limit:
		return name, NameTooLong, 0
	}
	if r, found := FirstHidden(name); found {
		return name, NameHidden, r
	}
	if !HasGlyph(name) {
		return name, NameNoGlyph, 0
	}
	return name, NameOK, 0
}
