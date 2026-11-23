package query

import (
	"context"
	"time"

	"quizzivy/internal/modules/organization/application/internal/support"
	"quizzivy/internal/modules/organization/application/model"
	"quizzivy/internal/modules/organization/domain"
)

// CurrentTerm asks for the term that holds the calendar day of On, read in
// On's own location: the current term when On is today in the caller's zone,
// and the term a date falls in otherwise, such as the term that holds a weekly
// series' first date. Terms never overlap, so there is one at most; the
// answer is nil when no term holds the day.
type CurrentTerm struct {
	On time.Time
}

type CurrentTermHandler struct {
	*support.Service
}

func (h CurrentTermHandler) Handle(ctx context.Context, q CurrentTerm) (*model.Term, error) {
	terms, err := h.TermStore()
	if err != nil {
		return nil, err
	}
	t, err := terms.On(ctx, domain.Day(q.On))
	if err != nil || t == nil {
		return nil, err
	}
	out := modelOf(*t)
	return &out, nil
}

// TermsEndingFrom asks for the terms whose last day is the calendar day of On
// or later, read as CurrentTerm reads it, earliest first: the terms a
// calendar from On onwards can meet.
type TermsEndingFrom struct {
	On time.Time
}

type TermsEndingFromHandler struct {
	*support.Service
}

func (h TermsEndingFromHandler) Handle(ctx context.Context, q TermsEndingFrom) ([]model.Term, error) {
	terms, err := h.TermStore()
	if err != nil {
		return nil, err
	}
	stored, err := terms.EndingFrom(ctx, domain.Day(q.On))
	if err != nil {
		return nil, err
	}
	out := make([]model.Term, len(stored))
	for i, t := range stored {
		out[i] = modelOf(t)
	}
	return out, nil
}

func modelOf(t domain.Term) model.Term {
	return model.Term{ID: t.ID, Name: t.Name, StartsOn: t.StartsOn, EndsOn: t.EndsOn}
}
