package domain

import (
	"encoding/base64"
	"strings"

	"github.com/google/uuid"
)

// Audience is whom the New message picker lists (listMessageRecipients).
type Audience string

const (
	// AudienceStudents is every enabled student (F2, T-F2.3).
	AudienceStudents Audience = "students"
	// AudienceColleagues is every enabled account that opens the teacher
	// workspace and is not a student (T-F2.4).
	AudienceColleagues Audience = "colleagues"
)

// The picker's page sizes, as the contract's limit.
const (
	DefaultRecipients = 30
	MaxRecipients     = 50
)

// RecipientFilter is what the picker reads: the caller, whose class scope
// (their own classes, or every class under scope.all) bounds ClassID and
// Mine and nothing else; a search on the name; at most one of the students'
// filters; and the page after the row After names.
type RecipientFilter struct {
	Audience  Audience
	ActorID   string
	All       bool
	Query     string
	ClassID   string
	Mine      bool
	InNoClass bool
	After     string
	Limit     int
}

// Check refuses what the contract answers 400 (ErrInvalid): a class with
// mine or in no class, mine with in no class, any students' filter for
// colleagues, and an audience the picker does not know. It clamps Limit to
// 1..MaxRecipients, DefaultRecipients when unset.
func (f RecipientFilter) Check() (RecipientFilter, error) {
	filters := 0
	for _, on := range []bool{f.ClassID != "", f.Mine, f.InNoClass} {
		if on {
			filters++
		}
	}
	switch {
	case f.Audience != AudienceStudents && f.Audience != AudienceColleagues:
		return RecipientFilter{}, ErrInvalid
	case filters > 1, f.Audience == AudienceColleagues && filters > 0:
		return RecipientFilter{}, ErrInvalid
	}
	f.Query = strings.TrimSpace(f.Query)
	if f.Limit < 1 {
		f.Limit = DefaultRecipients
	}
	if f.Limit > MaxRecipients {
		f.Limit = MaxRecipients
	}
	return f, nil
}

// Recipient is a row of the picker. A student's ClassNames are the caller's
// current classes they are in, not archived, and OtherClassNames the rest of
// their current classes, by name only; both empty for a student in no class
// and for a colleague. A colleague's RoleName and RoleBuiltinKey are their
// role's stored name and built-in key (nil for a custom role); a student's
// are nil. ThreadExists is the caller's own thread with the row.
type Recipient struct {
	ID              string
	Name            string
	AvatarKey       *string
	AvatarURL       *string
	ClassNames      []string
	OtherClassNames []string
	RoleName        *string
	RoleBuiltinKey  *string
	ThreadExists    bool
	CanPost         bool
}

// Recipients is one page of the picker, ordered by folded name and id, and
// the cursor of the next page, empty on the last.
type Recipients struct {
	Rows []Recipient
	Next string
}

// recipientCursor marks the picker's cursors, so a cursor another list gave
// is refused rather than read.
const recipientCursor = "recipients:"

// RecipientCursor is the opaque cursor of the page after the row id: the
// next page starts after that row's (folded name, id), read again from the
// account, so the cursor stays a fixed size whatever the name. The row is
// read again only while it is one of the audience's own rows, so a cursor
// naming any other account ends the list rather than placing it.
func RecipientCursor(id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(recipientCursor + id))
}

// ParseRecipientCursor returns the row id a RecipientCursor names, or
// ErrInvalid for a cursor the picker did not give.
func ParseRecipientCursor(cursor string) (string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return "", ErrInvalid
	}
	id, ok := strings.CutPrefix(string(raw), recipientCursor)
	if !ok {
		return "", ErrInvalid
	}
	parsed, err := uuid.Parse(id)
	if err != nil || parsed.String() != id {
		return "", ErrInvalid
	}
	return id, nil
}
