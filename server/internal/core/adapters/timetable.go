package adapters

import (
	"context"

	assignmentsquery "quizzivy/internal/modules/assignments/application/query"
	assignmentsdomain "quizzivy/internal/modules/assignments/domain"
	timetableports "quizzivy/internal/modules/timetable/application/ports"
	timetabledomain "quizzivy/internal/modules/timetable/domain"
	"quizzivy/internal/shared/cqrs"
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
