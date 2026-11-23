// Package query is the timetable's reads. None of them writes.
package query

import (
	"context"
	"time"

	"quizzivy/internal/modules/timetable/application/internal/support"
	"quizzivy/internal/modules/timetable/domain"
	"quizzivy/internal/shared/access"
)

// Occurrences asks for the sessions that start in [From, To) in the classes
// Scope reaches, all of them or only ClassIDs when it is not nil, with each
// one-off's date read in ViewerID's zone. It costs two queries whatever the
// number of classes and series: the series, then the rows.
type Occurrences struct {
	Scope    access.Scope
	ViewerID string
	ClassIDs []string
	From, To time.Time
}

type OccurrencesHandler struct {
	*support.Service
}

func (h OccurrencesHandler) Handle(ctx context.Context, q Occurrences) ([]domain.Session, error) {
	manager, err := h.Viewer(ctx, q.ViewerID)
	if err != nil {
		return nil, err
	}
	w := domain.Window{Scope: q.Scope, ClassIDs: q.ClassIDs, From: q.From, To: q.To}
	series, err := h.Repo.Series(ctx, w)
	if err != nil {
		return nil, err
	}
	rows, err := h.Repo.Rows(ctx, w)
	if err != nil {
		return nil, err
	}
	return manager.Expand(series, rows, q.From, q.To), nil
}
