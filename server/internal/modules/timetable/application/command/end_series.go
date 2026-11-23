package command

import (
	"context"
	"time"

	"quizzivy/internal/modules/timetable/application/internal/support"
	"quizzivy/internal/modules/timetable/domain"
	"quizzivy/internal/shared/actor"
)

// EndSeries makes LastDate the last day series SeriesID meets, for Actor, and
// deletes its per-date rows after it; a LastDate before the series' first date
// removes the series, and the answer is nil. An occurrence after LastDate with
// attendance fails with domain.ErrHasAttendance and nothing changes; a
// LastDate more than 731 days after the first date fails with a
// *validation.Error naming lastDate. One session_series.ended audit row.
type EndSeries struct {
	Actor    actor.Actor
	SeriesID string
	LastDate time.Time
}

type EndSeriesHandler struct {
	*support.Service
}

func (h EndSeriesHandler) Handle(ctx context.Context, cmd EndSeries) (*domain.Series, error) {
	return h.Repo.EndSeries(ctx, cmd.Actor.Scope, cmd.SeriesID, func(s domain.Series) (domain.Ending, error) {
		return s.EndOn(cmd.LastDate)
	}, h.WriterOf(cmd.Actor))
}
