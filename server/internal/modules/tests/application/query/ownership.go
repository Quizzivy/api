package query

import (
	"context"

	"quizzivy/internal/modules/tests/application/internal/support"
	"quizzivy/internal/modules/tests/domain"
	"quizzivy/internal/shared/access"
)

// Ownership reads the title and owner of the live test ID that Scope may
// read; any other test is domain.ErrNotFound.
type Ownership struct {
	ID    string
	Scope access.Scope
}

type OwnershipHandler struct {
	*support.Service
}

func (s OwnershipHandler) Handle(ctx context.Context, q Ownership) (domain.Ownership, error) {
	return s.Repo.Ownership(ctx, q.Scope, q.ID)
}
