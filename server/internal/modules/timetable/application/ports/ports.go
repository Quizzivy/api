// Package ports is what the timetable needs from outside it: the term that
// holds a date and the terms ahead, from the organization module, the
// calendar zone of an account's profile, from identity, and the assignment
// windows the calendar draws and the assignments a student's week lists as
// due, from assignments.
package ports

import (
	"context"
	"time"

	organizationmodel "quizzivy/internal/modules/organization/application/model"
	"quizzivy/internal/modules/timetable/domain"
	"quizzivy/internal/shared/access"
)

// Terms answers the term that holds the calendar day of on, read in on's own
// location, or nil when no term holds it (adapters.Terms).
type Terms interface {
	CurrentTerm(ctx context.Context, on time.Time) (*organizationmodel.Term, error)
}

// Zones resolves the calendar zone an account's profile names, the centre's
// when it names none (adapters.ProfileZone).
type Zones interface {
	ZoneOf(ctx context.Context, userID string) (string, error)
}

// TermsAhead lists every term whose last day is the calendar day of on or
// later, earliest first, for the calendar (adapters.Terms).
type TermsAhead interface {
	TermsEndingFrom(ctx context.Context, on time.Time) ([]organizationmodel.Term, error)
}

// AssignmentWindows asks the assignments module for the published assignments
// Scope reaches whose own window opens or closes in [From, To), only those
// targeting class ClassID when it is not empty.
type AssignmentWindows struct {
	Scope    access.Scope
	ClassID  string
	From, To time.Time
}

// Assignments reads assignment windows for the calendar from the assignments
// module's CalendarWindows query (adapters.TimetableAssignments), in one query
// whatever the number of classes. Per-student overrides are not read.
type Assignments interface {
	Windows(ctx context.Context, q AssignmentWindows) ([]domain.AssignmentWindow, error)
}

// DueItems reads the assignments that target a student, through the
// assignments module's student query, ForStudent (adapters.TimetableDueItems):
// the ones they may still take or are taking at now, each with the student's
// own close and time limit, their override included. The timetable keeps the
// ones its week holds (domain.StudentWeek).
type DueItems interface {
	Due(ctx context.Context, studentID string, now time.Time) ([]domain.DueAssignment, error)
}
