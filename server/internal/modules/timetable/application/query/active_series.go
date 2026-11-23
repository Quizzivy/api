package query

import (
	"context"
	"time"

	"quizzivy/internal/modules/timetable/application/internal/support"
	"quizzivy/internal/modules/timetable/domain"
)

// ActiveSeries asks for the weekly series of ClassIDs that are active at Now,
// each read in its own zone (Series.ActiveOn): what a class's schedule line is
// derived from (T-R8.7). It costs one query whatever the number of classes,
// none for no classes, and applies no scope: the caller names classes it has
// reached already, through its scope, its membership or a valid join code.
type ActiveSeries struct {
	ClassIDs []string
	Now      time.Time
}

type ActiveSeriesHandler struct {
	*support.Service
}

func (h ActiveSeriesHandler) Handle(ctx context.Context, q ActiveSeries) ([]domain.Series, error) {
	if len(q.ClassIDs) == 0 {
		return nil, nil
	}
	found, err := h.Repo.ActiveSeries(ctx, q.ClassIDs, q.Now)
	if err != nil {
		return nil, err
	}
	active := found[:0]
	for _, s := range found {
		if s.ActiveOn(q.Now) {
			active = append(active, s)
		}
	}
	return active, nil
}
