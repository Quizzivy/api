// Package domain is the reporting context: read models over the other
// modules' data for the teacher's home. It owns no aggregate.
package domain

import (
	"errors"
	"slices"
	"time"
)

// Summary is the teacher home: its queue figures and its calendar readings.
type Summary struct {
	Home
	NewestFlaggedAttempt *FlaggedAttempt
	ClosingSoon          int
	WaitingStudents      int
	OldestWaitingAt      *time.Time
	NextClosing          *ClosingAssignment
	AwaitingGrading      int
	FlaggedAttempts      int
}

// FlaggedAttempt identifies one reachable flagged paper and its assignment.
type FlaggedAttempt struct{ AssignmentID, AttemptID string }

// Recent is one attempt as the teacher's queues list it.
type Recent struct {
	ID            string
	StudentID     string
	StudentName   string
	AssignmentID  string
	TestTitle     string
	Status        string
	SubmittedAt   *time.Time
	PendingManual int
	Flagged       bool
}

// ClosingAssignment is the nearest open assignment due within 24 hours.
type ClosingAssignment struct {
	ID             string
	Title          string
	ClosesAt       time.Time
	SubmittedCount int
	TargetCount    int
}

// ErrNotificationsUnavailable means the nav summary has no notifications port.
var ErrNotificationsUnavailable = errors.New("dashboard: notifications unavailable")

// Home is the calendar and activity reading of the teacher dashboard.
type Home struct {
	TakingNow      TakingNow
	Submissions    Submissions
	Today          []Today
	RecentActivity []Activity
}

// TakingNow counts distinct students and assignments before their attempt deadlines.
type TakingNow struct {
	Students, Assignments int
	AssignmentID          *string
}

// Submissions is a complete calendar series and the graded mean in that range.
type Submissions struct {
	Days           []Day
	Total          int
	AveragePercent *int
}

// Day is a local calendar date and its handed-in paper count.
type Day struct {
	Date  string
	Count int
}

// Today is one item of the actor's day: a published assignment opening or
// effective closing (Kind opens or closes), or a session of a class the actor
// teaches (Kind session, with Session).
type Today struct {
	Kind                string
	At                  time.Time
	AssignmentID, Title string
	NotSubmitted        int
	Session             *TodaySession
}

// TodaySession is a session of today: its opaque id, its class and the room
// it meets in, nil for none.
type TodaySession struct {
	ID, ClassID, ClassName string
	Room                   *string
}

// TodaySessionKind is the kind of a Today item that is a session.
const TodaySessionKind = "session"

// MaxToday is the most items the day lists (DashboardToday's maxItems).
const MaxToday = 40

// DayOf is the instants of the calendar day that holds now in loc: its
// midnight to the next day's.
func DayOf(now time.Time, loc *time.Location) (time.Time, time.Time) {
	local := now.In(loc)
	start := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
	return start, time.Date(local.Year(), local.Month(), local.Day()+1, 0, 0, 0, 0, loc)
}

// MergeToday is the day's assignment items and sessions in one list, oldest
// first by At, an assignment item before a session at the same instant and
// each kept in its own order otherwise, cut to MaxToday.
func MergeToday(assignments, sessions []Today) []Today {
	out := make([]Today, 0, len(assignments)+len(sessions))
	out = append(append(out, assignments...), sessions...)
	slices.SortStableFunc(out, func(a, b Today) int { return a.At.Compare(b.At) })
	if len(out) > MaxToday {
		out = out[:MaxToday]
	}
	return out
}

// Activity is one attempt state or a student-like class join by code.
type Activity struct {
	Kind                 string
	At                   time.Time
	StudentName, Subject string
	Flagged              bool
}

// Nav is the caller's permission-aware shell counts. SharedWithMe counts the
// live grants to the caller, and UnreadMessages the messages past the
// caller's read pointers, not their own, over their threads.
type Nav struct {
	LiveAssignments, AnswersToGrade *int
	UnreadNotifications             int
	SharedWithMe                    int
	UnreadMessages                  int
}
