package query

import (
	"context"
	"time"

	"quizzivy/internal/modules/timetable/application/internal/support"
	"quizzivy/internal/modules/timetable/domain"
	"quizzivy/internal/shared/access"
)

// AttendanceSessions asks for a page of the sessions that take a register in
// the classes Scope reaches, or only ClassID when it is set: started (from
// their start) and not cancelled, newest first, kept by Filter. A ClassID
// outside Scope answers domain.ErrNotFound, as an unknown one does.
//
// The sessions are computed, an occurrence having no row until a write needs
// one, so the read expands every series of the classes from its first day to
// now: NotMarked counts every started session without attendance, whatever
// the page, so the whole history is read in any case. It costs at most five
// queries whatever the page, the classes and the series: whether ClassID is
// reached (when it is set), the first day of the timetable, the series, the
// rows, and the page's student counts and marks.
type AttendanceSessions struct {
	Scope   access.Scope
	ClassID *string
	Filter  domain.SessionFilter
}

type AttendanceSessionsHandler struct {
	*support.Service
}

func (h AttendanceSessionsHandler) Handle(ctx context.Context, q AttendanceSessions) (domain.SessionPage, error) {
	w := domain.Window{Scope: q.Scope}
	if q.ClassID != nil {
		if err := h.Repo.Reaches(ctx, q.Scope, *q.ClassID); err != nil {
			return domain.SessionPage{}, err
		}
		w.ClassIDs = []string{*q.ClassID}
	}
	now := h.Now()
	first, err := h.Repo.Earliest(ctx, w)
	if err != nil || first == nil {
		return domain.SessionPage{Items: []domain.ListedSession{}}, err
	}
	// A day either side of the first day covers every zone; the read ends
	// just after now, since a session that starts now has started.
	w.From, w.To = first.AddDate(0, 0, -1), now.Truncate(time.Microsecond).Add(time.Microsecond)
	series, err := h.Repo.Series(ctx, w)
	if err != nil {
		return domain.SessionPage{}, err
	}
	rows, err := h.Repo.Rows(ctx, w)
	if err != nil {
		return domain.SessionPage{}, err
	}
	page := domain.PageOf(domain.Started(domain.OccurrenceManager{}.Expand(series, rows, w.From, w.To), now), q.Filter)
	if len(page.Items) == 0 {
		return page, nil
	}
	sessions := make([]domain.Session, len(page.Items))
	for i, item := range page.Items {
		sessions[i] = item.Session
	}
	facts, err := h.Repo.Facts(ctx, sessions)
	if err != nil {
		return domain.SessionPage{}, err
	}
	for i := range page.Items {
		page.Items[i].SessionFacts = facts[page.Items[i].ID.String()]
	}
	return page, nil
}
