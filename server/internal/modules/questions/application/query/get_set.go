package query

import (
	"context"

	"quizzivy/internal/modules/questions/application/internal/support"
	"quizzivy/internal/modules/questions/domain"
	"quizzivy/internal/shared/access"
)

// GetSet reads one live question set Scope can read; any other answers
// ErrSetNotFound, exactly as a missing one does.
type GetSet struct {
	ID    string
	Scope access.Scope
}

type GetSetHandler struct {
	*support.Service
}

func (s GetSetHandler) Handle(ctx context.Context, q GetSet) (domain.QuestionSet, error) {
	return s.Sets.GetSet(ctx, q.Scope, q.ID)
}
