package command

import (
	"context"
	"quizzivy/internal/modules/questions/application/internal/support"
	"quizzivy/internal/modules/questions/domain"
)

// Update edits a bank question. With SetID the edit is made in that set's
// context, which the actor must be able to edit and which must hold the
// question; the question's owner and scope.all edit it in place.
type Update struct {
	Request domain.WriteRequest
	SetID   string
}

type UpdateHandler struct {
	*support.Service
}

func (s UpdateHandler) Handle(ctx context.Context, cmd Update) (domain.Question, error) {
	if cmd.Request.ID == "" {
		return domain.Question{}, domain.ErrNotFound
	}
	if cmd.SetID != "" {
		if s.Contexts == nil {
			return domain.Question{}, domain.ErrSetNotFound
		}
		if err := s.Contexts.SetHolds(ctx, cmd.Request.Scope(), cmd.SetID, cmd.Request.ID); err != nil {
			return domain.Question{}, err
		}
	}
	input, err := cmd.Request.Input.Composed()
	if err != nil {
		return domain.Question{}, err
	}
	cmd.Request.Input = input
	if err := cmd.Request.Input.ValidateAuthoring(); err != nil {
		return domain.Question{}, err
	}
	return s.Write(ctx, cmd.Request, true)
}
