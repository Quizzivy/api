package command

import (
	"context"

	"quizzivy/internal/modules/questions/application/internal/support"
	"quizzivy/internal/modules/questions/domain"
	"quizzivy/internal/shared/cqrs"
)

// DeleteSet deletes a question set and every grant to it, for its owner. Its
// questions stay in the bank.
type DeleteSet struct {
	Write domain.SetWrite
}

type DeleteSetHandler struct {
	*support.Service
}

func (s DeleteSetHandler) Handle(ctx context.Context, cmd DeleteSet) (cqrs.Nothing, error) {
	cmd.Write.Now = s.Now()
	return cqrs.Nothing{}, s.Sets.DeleteSet(ctx, cmd.Write)
}
