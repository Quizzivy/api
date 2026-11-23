package query

import (
	"context"
	"fmt"
	"time"

	"quizzivy/internal/modules/timetable/application/internal/support"
	"quizzivy/internal/modules/timetable/application/ports"
	"quizzivy/internal/modules/timetable/domain"
	"quizzivy/internal/shared/access"
)

// StudentWeek asks for StudentID's week (getStudentWeek): the ISO week, read
// in the student's zone, that holds the date Start, or today when Start is
// nil.
type StudentWeek struct {
	StudentID string
	Start     *time.Time
}

// StudentWeekHandler reads a student's week: their zone, the classes they are
// an active member of that are not archived, then one query for those
// classes' series and one for their rows, whatever their number, and the
// assignments due through Due. Without Due the week lists no assignment.
// Reading writes nothing.
type StudentWeekHandler struct {
	*support.Service
	Due ports.DueItems
}

func (h StudentWeekHandler) Handle(ctx context.Context, q StudentWeek) (domain.Week, error) {
	zone, err := h.ZoneOf(ctx, q.StudentID)
	if err != nil {
		return domain.Week{}, err
	}
	loc, now := domain.LocationOf(zone), h.Now()
	on := domain.Today(now, loc)
	if q.Start != nil {
		on = domain.Day(*q.Start)
	}
	monday := domain.MondayOf(on)
	from, to := domain.WeekRange(monday, loc)
	sessions, err := h.sessions(ctx, q.StudentID, loc, from, to)
	if err != nil {
		return domain.Week{}, err
	}
	var due []domain.DueAssignment
	if h.Due != nil {
		if due, err = h.Due.Due(ctx, q.StudentID, now); err != nil {
			return domain.Week{}, fmt.Errorf("timetable: the assignments due for %s: %w", q.StudentID, err)
		}
	}
	return domain.StudentWeek(monday, loc, sessions, due), nil
}

// sessions are the sessions of the student's classes in [from, to). The
// classes are chosen by membership, so the loaders read them by id in every
// teacher's scope; a student in no class reads nothing more.
func (h StudentWeekHandler) sessions(ctx context.Context, studentID string, loc *time.Location, from, to time.Time) ([]domain.Session, error) {
	classes, err := h.Repo.MemberClasses(ctx, studentID)
	if err != nil || len(classes) == 0 {
		return nil, err
	}
	w := domain.Window{Scope: access.Scope{All: true}, ClassIDs: classes, From: from, To: to}
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
