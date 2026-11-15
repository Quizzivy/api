package query

import (
	"context"

	"quizzivy/internal/modules/leads/application/internal/support"
	"quizzivy/internal/modules/leads/domain"
)

// Get reads the request ID, or fails with domain.ErrNotFound.
type Get struct {
	ID string
}

type GetHandler struct {
	*support.Service
}

func (h GetHandler) Handle(ctx context.Context, q Get) (domain.Lead, error) {
	return h.Repo.Get(ctx, q.ID)
}
