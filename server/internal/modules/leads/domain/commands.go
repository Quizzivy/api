package domain

import (
	"time"
	"unicode/utf8"

	"quizzivy/internal/shared/validation"
)

// Submit is a Submission to judge and, when it is kept, to store as received
// and consented to at Now.
type Submit struct {
	Submission Submission
	Now        time.Time
}

// Change is what an Admin asks to change on a request. A nil Status keeps the
// status. Note is read only when NoteSet, and an empty one removes the note.
type Change struct {
	Status  *Status
	NoteSet bool
	Note    string
}

// Normalised returns the change as it would be stored: the note in Unicode
// NFC and trimmed. It refuses, with a *validation.Error naming note, a note
// longer than MaxNote characters.
func (c Change) Normalised() (Change, error) {
	out := c
	if !c.NoteSet {
		out.Note = ""
		return out, nil
	}
	out.Note = validation.Tidy(c.Note)
	if utf8.RuneCountInString(out.Note) > MaxNote {
		return Change{}, validation.Refuse(FieldNote, "Ghi chú không được dài quá 1000 ký tự.", "The note cannot be longer than 1000 characters.")
	}
	return out, nil
}

// Update is a Change to the request ID, by whom and from where.
type Update struct {
	ID        string
	Change    Change
	ActorID   string
	IP        string
	UserAgent string
	Now       time.Time
}

// Delete removes the request ID, by whom and from where.
type Delete struct {
	ID        string
	ActorID   string
	IP        string
	UserAgent string
	Now       time.Time
}

// ListQuery is one page of the requests, newest first. An empty Status reads
// every status. Q matches the name, without regard to case or accents, or the
// phone number by the digits of Q.
type ListQuery struct {
	Status Status
	Q      string
	Page   int
	Limit  int
}

// The page sizes of the list.
const (
	DefaultPageSize = 20
	MaxPageSize     = 50
)
