package command

import (
	"context"
	"time"

	"quizzivy/internal/modules/timetable/application/internal/support"
	"quizzivy/internal/modules/timetable/domain"
	"quizzivy/internal/shared/actor"
	"quizzivy/internal/shared/content"
)

// UpdateSeries applies Change to series SeriesID from FromDate on, for Actor,
// and returns the series that carries it with the number of per-date changes
// dropped. FromDate is read in the series' zone: before today there, or after
// the series' last date, it fails with a *validation.Error naming fromDate;
// when today's occurrence has attendance the change starts tomorrow. The old
// series ends the day before and a new one starts then, unless none of it has
// come yet, when it changes in place. Per-date rows of the old series from
// then on without attendance are deleted; rows with attendance stay. A change
// that alters nothing writes nothing. One session_series.updated audit row.
type UpdateSeries struct {
	Actor    actor.Actor
	SeriesID string
	FromDate time.Time
	Change   domain.SeriesChange
}

type UpdateSeriesHandler struct {
	*support.Service
}

func (h UpdateSeriesHandler) Handle(ctx context.Context, cmd UpdateSeries) (domain.SeriesEdit, error) {
	change := cmd.Change
	var c content.Composer
	change.Room.Value = domain.Text(&c, "room", change.Room.Value, domain.MaxRoom)
	if err := c.Err(); err != nil {
		return domain.SeriesEdit{}, err
	}
	now := h.Now()
	return h.Repo.EditSeries(ctx, cmd.Actor.Scope, cmd.SeriesID, func(s domain.Series, attendedOn func(time.Time) bool) (domain.Split, error) {
		today := domain.Today(now, s.Location())
		return s.SplitAt(change, cmd.FromDate, today, attendedOn(today))
	}, h.WriterOf(cmd.Actor))
}
