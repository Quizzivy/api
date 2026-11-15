package query

import (
	"context"
	"strings"

	"quizzivy/internal/modules/leads/application/internal/support"
	"quizzivy/internal/modules/leads/domain"
	"quizzivy/internal/shared/paging"
)

// List reads one page of the requests, newest first. An empty Status reads
// every status; Q, trimmed, matches the name or the digits of the number.
type List struct {
	Status domain.Status
	Q      string
	Page   int
	Limit  int
}

// Listed is a page of requests and where it sits among those that match.
type Listed struct {
	Leads []domain.Lead
	Page  paging.Page
}

type ListHandler struct {
	*support.Service
}

func (h ListHandler) Handle(ctx context.Context, q List) (Listed, error) {
	leads, page, err := h.Repo.List(ctx, domain.ListQuery{Status: q.Status, Q: strings.TrimSpace(q.Q), Page: q.Page, Limit: q.Limit})
	if err != nil {
		return Listed{}, err
	}
	return Listed{Leads: leads, Page: page}, nil
}
