package query

import (
	"context"

	"quizzivy/internal/modules/organization/application/internal/support"
	"quizzivy/internal/modules/organization/domain"
)

// ListTerms asks for every term, newest first, each with its state on the
// viewer's today: the calendar day it is now in the zone their profile names,
// else domain.DefaultZone.
type ListTerms struct {
	ViewerID string
}

type ListTermsHandler struct {
	*support.Service
}

func (h ListTermsHandler) Handle(ctx context.Context, q ListTerms) ([]domain.StatedTerm, error) {
	terms, err := h.TermStore()
	if err != nil {
		return nil, err
	}
	today, err := h.TodayOf(ctx, q.ViewerID)
	if err != nil {
		return nil, err
	}
	stored, err := terms.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]domain.StatedTerm, len(stored))
	for i, t := range stored {
		out[i] = domain.StatedTerm{Term: t, State: t.StateOn(today)}
	}
	return out, nil
}
