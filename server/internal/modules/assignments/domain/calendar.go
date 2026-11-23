package domain

import (
	"time"

	"quizzivy/internal/shared/access"
)

// CalendarInput asks for the published assignments Scope reaches whose window
// opens or closes in [From, To): all of them, or only those targeting class
// ClassID when it is not empty.
type CalendarInput struct {
	Scope    access.Scope
	ClassID  string
	From, To time.Time
}

// CalendarWindow is one assignment's own window as a calendar draws it: when
// it opens and when it stops taking attempts (its early close or the end of
// its window, whichever comes first), with no student's override. ClassNames
// are its target classes the reader reaches, by name.
type CalendarWindow struct {
	ID         string
	Title      string
	OpensAt    time.Time
	ClosesAt   time.Time
	ClassNames []string
}
