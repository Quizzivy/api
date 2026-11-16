package command

import (
	"context"
	"quizzivy/internal/modules/assignments/application/internal/support"
	"quizzivy/internal/modules/assignments/domain"
	"quizzivy/internal/shared/content"
)

type Update struct {
	Request domain.Request
	Input   domain.WriteInput
}

type UpdateHandler struct {
	*support.Service
}

func (s UpdateHandler) Handle(ctx context.Context, cmd Update) (domain.Assignment, error) {
	cmd.Input.StudentNote = content.NFCPtr(cmd.Input.StudentNote)
	updated, err := s.Repo.Update(ctx, cmd.Request, cmd.Input)
	if err == nil {
		s.Nudge()
	}
	return updated, err
}
