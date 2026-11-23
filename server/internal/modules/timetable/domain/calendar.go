package domain

import (
	"slices"
	"strings"
	"time"

	"quizzivy/internal/shared/validation"
)

// MaxCalendarDays is the most days one calendar read covers: six weeks, the
// longest view the Calendar draws.
const MaxCalendarDays = 42

// CalendarRange is the instants the local dates [from, to) cover in loc: from's
// midnight to to's, each built with time.Date, so a day that is not 24 hours
// long in loc is read as the zone database has it. to must follow from by 1 to
// MaxCalendarDays days, else a *validation.Error names to.
func CalendarRange(from, to time.Time, loc *time.Location) (time.Time, time.Time, error) {
	first, end := Day(from), Day(to)
	if days := int(end.Sub(first).Hours() / 24); days < 1 || days > MaxCalendarDays {
		return time.Time{}, time.Time{}, validation.Refuse("to",
			"Khoảng thời gian phải dài từ 1 đến 42 ngày.", "The end must come 1 to 42 days after the start.")
	}
	midnight := Clock{}
	return midnight.On(first, loc), midnight.On(end, loc), nil
}

// AssignmentWindow is one assignment's own window as the assignments module
// reads it for a calendar: when it opens and when it stops taking attempts,
// with no student's override. ClassNames are its target classes the reader
// reaches, by name.
type AssignmentWindow struct {
	ID         string
	Title      string
	OpensAt    time.Time
	ClosesAt   time.Time
	ClassNames []string
}

// EventKind is whether an assignment event is its opening or its closing.
type EventKind string

// The two assignment events.
const (
	EventOpens  EventKind = "opens"
	EventCloses EventKind = "closes"
)

// AssignmentEvent is an assignment opening or closing at At.
type AssignmentEvent struct {
	Kind         EventKind
	At           time.Time
	AssignmentID string
	Title        string
	ClassNames   []string
}

// Events is every opening and closing of windows that falls in [from, to),
// ordered by At, then a closing before an opening, then by assignment. A
// window that closes before it opens, closed early before its start, draws
// only its close: it never opened.
func Events(windows []AssignmentWindow, from, to time.Time) []AssignmentEvent {
	out := []AssignmentEvent{}
	for _, w := range windows {
		event := AssignmentEvent{AssignmentID: w.ID, Title: w.Title, ClassNames: w.ClassNames}
		if event.ClassNames == nil {
			event.ClassNames = []string{}
		}
		if in(w.OpensAt, from, to) && w.ClosesAt.After(w.OpensAt) {
			event.Kind, event.At = EventOpens, w.OpensAt
			out = append(out, event)
		}
		if in(w.ClosesAt, from, to) {
			event.Kind, event.At = EventCloses, w.ClosesAt
			out = append(out, event)
		}
	}
	slices.SortFunc(out, func(a, b AssignmentEvent) int {
		if c := a.At.Compare(b.At); c != 0 {
			return c
		}
		if a.Kind != b.Kind {
			return strings.Compare(string(a.Kind), string(b.Kind))
		}
		return strings.Compare(a.AssignmentID, b.AssignmentID)
	})
	return out
}
