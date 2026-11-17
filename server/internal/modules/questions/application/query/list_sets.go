package query

import (
	"context"

	"quizzivy/internal/modules/questions/application/internal/support"
	"quizzivy/internal/modules/questions/domain"
	"quizzivy/internal/shared/access"
)

// ListSets reads the question sets Scope.UserID owns, most recently changed
// first.
type ListSets struct {
	Scope access.Scope
}

type ListSetsHandler struct {
	*support.Service
}

func (s ListSetsHandler) Handle(ctx context.Context, q ListSets) ([]domain.QuestionSet, error) {
	return s.Sets.ListSets(ctx, q.Scope)
}
