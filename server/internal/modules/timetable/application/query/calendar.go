package query

import (
	"context"
	"fmt"
	"time"

	organizationmodel "quizzivy/internal/modules/organization/application/model"
	"quizzivy/internal/modules/timetable/application/internal/support"
	"quizzivy/internal/modules/timetable/application/ports"
	"quizzivy/internal/modules/timetable/domain"
	"quizzivy/internal/shared/access"
)

// Calendar asks for what Scope teaches between the local dates From
// (inclusive) and To (exclusive), read in ViewerID's zone: the sessions of the
// classes Scope reaches, cancelled ones included, the openings and closings of
// the assignments it reaches, and every term that ends on From or later. With
// ClassID, only that class's sessions and the events of the assignments that
// target it; a class Scope does not reach answers domain.ErrNotFound, as a
// missing one does. To must follow From by 1 to domain.MaxCalendarDays days,
// else a *validation.Error names to.
type Calendar struct {
	Scope    access.Scope
	ViewerID string
	ClassID  string
	From, To time.Time
}

// CalendarView is the calendar of a range.
type CalendarView struct {
	Sessions []domain.Session
	Events   []domain.AssignmentEvent
	Terms    []organizationmodel.Term
}

// CalendarHandler reads the calendar: the zone, the class when one is named,
// then one query for the series, one for the rows and one for the assignment
// windows, whatever the number of classes, and the terms. Without Terms it
// lists none, and without Assignments it draws no event.
type CalendarHandler struct {
	*support.Service
	Terms       ports.TermsAhead
	Assignments ports.Assignments
}

func (h CalendarHandler) Handle(ctx context.Context, q Calendar) (CalendarView, error) {
	zone, err := h.ZoneOf(ctx, q.ViewerID)
	if err != nil {
		return CalendarView{}, err
	}
	loc := domain.LocationOf(zone)
	from, to, err := domain.CalendarRange(q.From, q.To, loc)
	if err != nil {
		return CalendarView{}, err
	}
	var out CalendarView
	if out.Sessions, err = h.sessions(ctx, q, loc, from, to); err != nil {
		return CalendarView{}, err
	}
	if out.Events, err = h.events(ctx, q, from, to); err != nil {
		return CalendarView{}, err
	}
	if h.Terms != nil {
		if out.Terms, err = h.Terms.TermsEndingFrom(ctx, domain.Day(q.From)); err != nil {
			return CalendarView{}, fmt.Errorf("timetable: the terms from %s: %w", domain.Day(q.From).Format("2006-01-02"), err)
		}
	}
	return out, nil
}

func (h CalendarHandler) sessions(ctx context.Context, q Calendar, loc *time.Location, from, to time.Time) ([]domain.Session, error) {
	w := domain.Window{Scope: q.Scope, From: from, To: to}
	if q.ClassID != "" {
		if err := h.Repo.Reaches(ctx, q.Scope, q.ClassID); err != nil {
			return nil, err
		}
		w.ClassIDs = []string{q.ClassID}
	}
	series, err := h.Repo.Series(ctx, w)
	if err != nil {
		return nil, err
	}
	rows, err := h.Repo.Rows(ctx, w)
	if err != nil {
		return nil, err
	}
	return domain.OccurrenceManager{Viewer: loc}.Expand(series, rows, from, to), nil
}

func (h CalendarHandler) events(ctx context.Context, q Calendar, from, to time.Time) ([]domain.AssignmentEvent, error) {
	if h.Assignments == nil {
		return []domain.AssignmentEvent{}, nil
	}
	windows, err := h.Assignments.Windows(ctx, ports.AssignmentWindows{Scope: q.Scope, ClassID: q.ClassID, From: from, To: to})
	if err != nil {
		return nil, fmt.Errorf("timetable: the assignment windows: %w", err)
	}
	return domain.Events(windows, from, to), nil
}
