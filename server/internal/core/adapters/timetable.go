package adapters

import (
	"context"
	"time"

	assignmentsquery "quizzivy/internal/modules/assignments/application/query"
	assignmentsdomain "quizzivy/internal/modules/assignments/domain"
	timetableports "quizzivy/internal/modules/timetable/application/ports"
	timetabledomain "quizzivy/internal/modules/timetable/domain"
	"quizzivy/internal/shared/cqrs"
	"quizzivy/internal/shared/schedule"
)

// TimetableAssignments is the assignments module's CalendarWindows query
// behind the timetable's Assignments port: the calendar (T-R8.5) draws the
// opening and closing of the assignments the caller's scope reaches, each on
// its own window, without the timetable reading assignments.
type TimetableAssignments struct {
	Query cqrs.QueryHandler[assignmentsquery.CalendarWindows, []assignmentsdomain.CalendarWindow]
}

// Windows reads the published assignments q's scope reaches whose own window
// opens or closes in q's range, in one query.
func (a TimetableAssignments) Windows(ctx context.Context, q timetableports.AssignmentWindows) ([]timetabledomain.AssignmentWindow, error) {
	windows, err := a.Query.Handle(ctx, assignmentsquery.CalendarWindows{Input: assignmentsdomain.CalendarInput{
		Scope: q.Scope, ClassID: q.ClassID, From: q.From, To: q.To,
	}})
	if err != nil {
		return nil, err
	}
	out := make([]timetabledomain.AssignmentWindow, len(windows))
	for i, w := range windows {
		out[i] = timetabledomain.AssignmentWindow{ID: w.ID, Title: w.Title, OpensAt: w.OpensAt, ClosesAt: w.ClosesAt, ClassNames: w.ClassNames}
	}
	return out, nil
}

// TimetableDueItems is the assignments module's student query, ForStudent, the
// one behind the student's assignment list, behind the timetable's DueItems
// port: a student's week (T-R8.8) lists the assignments due for them, each on
// the student's own close and time limit, without the timetable reading
// assignments. The completed section holds only papers handed in and is not
// read.
type TimetableDueItems struct {
	Query cqrs.QueryHandler[assignmentsquery.ForStudent, assignmentsdomain.StudentSections]
}

// Due reads the assignments targeting studentID that are open to them, under
// way or not open yet at now, in one query. A card's close and time limit are
// already the student's, their override applied; the close is its earlier
// close when the assignment was closed early, and a flexible assignment
// applies no time limit.
func (a TimetableDueItems) Due(ctx context.Context, studentID string, now time.Time) ([]timetabledomain.DueAssignment, error) {
	sections, err := a.Query.Handle(ctx, assignmentsquery.ForStudent{StudentID: studentID, Now: now})
	if err != nil {
		return nil, err
	}
	cards := append(append([]assignmentsdomain.StudentCard{}, sections.DueNow...), sections.Upcoming...)
	out := make([]timetabledomain.DueAssignment, len(cards))
	for i, c := range cards {
		out[i] = timetabledomain.DueAssignment{
			ID: c.ID, Title: c.TestTitle, ClosesAt: schedule.Close(c.ClosesAt, c.ClosedAt, nil),
			HandedIn: c.AttemptsUsed > 0, Live: c.HasLiveAttempt,
		}
		if c.Mode.OrStrict() != schedule.Flexible {
			limit := c.DurationMin
			out[i].DurationMinutes = &limit
		}
	}
	return out, nil
}
