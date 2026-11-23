package command

import (
	"context"
	"time"

	"quizzivy/internal/modules/timetable/application/internal/support"
	"quizzivy/internal/modules/timetable/domain"
	"quizzivy/internal/shared/actor"
)

// EndSeries makes LastDate the last day series SeriesID meets, for Actor, and
// deletes its per-date rows after it that it does not keep (moved to before
// then, or with attendance). On a series that has not begun, a LastDate
// before its first date removes it, and the answer is nil. An occurrence
// starting after LastDate with attendance fails with domain.ErrHasAttendance
// and nothing changes. A LastDate before yesterday in the series' zone, after
// its current last date, or more than 731 days after its first date fails
// with a *validation.Error naming lastDate. One session_series.ended audit
// row.
type EndSeries struct {
	Actor    actor.Actor
	SeriesID string
	LastDate time.Time
}

type EndSeriesHandler struct {
	*support.Service
}

func (h EndSeriesHandler) Handle(ctx context.Context, cmd EndSeries) (*domain.Series, error) {
	now := h.Now()
	return h.Repo.EndSeries(ctx, cmd.Actor.Scope, cmd.SeriesID, func(s domain.Series) (domain.Ending, error) {
		return s.EndOn(cmd.LastDate, domain.Today(now, s.Location()))
	}, h.WriterOf(cmd.Actor))
}
