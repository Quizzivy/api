package adapters

import (
	"context"
	"time"

	assignmentsquery "quizzivy/internal/modules/assignments/application/query"
	assignmentsdomain "quizzivy/internal/modules/assignments/domain"
	classesdomain "quizzivy/internal/modules/classes/domain"
	timetableports "quizzivy/internal/modules/timetable/application/ports"
	timetablequery "quizzivy/internal/modules/timetable/application/query"
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

// ClassSchedules is the timetable's ActiveSeries query behind the classes
// module's ScheduleSource port: every class surface and the join preview
// derive the schedule's slots and the room from the class's active weekly
// series (T-R8.7) without the classes module reading the timetable. One query
// answers a whole list page.
type ClassSchedules struct {
	Query cqrs.QueryHandler[timetablequery.ActiveSeries, []timetabledomain.Series]
}

// ActiveSeries reads the active series of classIDs at now, keyed by class:
// when each meets, read in its own zone, and its room.
func (a ClassSchedules) ActiveSeries(ctx context.Context, classIDs []string, now time.Time) (map[string][]classesdomain.WeeklySeries, error) {
	series, err := a.Query.Handle(ctx, timetablequery.ActiveSeries{ClassIDs: classIDs, Now: now})
	if err != nil {
		return nil, err
	}
	out := make(map[string][]classesdomain.WeeklySeries)
	for _, s := range series {
		out[s.ClassID] = append(out[s.ClassID], classesdomain.WeeklySeries{
			ScheduleSlot: classesdomain.ScheduleSlot{Weekday: int(s.Weekday), StartTime: s.Start.String(), DurationMinutes: s.DurationMinutes},
			Room:         s.Room,
		})
	}
	return out, nil
}
