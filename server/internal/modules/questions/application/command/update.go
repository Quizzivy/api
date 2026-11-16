package command

import (
	"context"
	"quizzivy/internal/modules/questions/application/internal/support"
	"quizzivy/internal/modules/questions/domain"
)

// Update edits a bank question. With SetID the edit is made in that set's
// context, which the actor must be able to edit and which must hold the
// question; the question's owner and scope.all edit it in place. Anyone else
// who reads it, through a test or a set shared with them, gets ErrReadOnly
// when they may only use it and ErrEditContextRequired when they may edit
// it, since such an edit is made in its test's or set's context (D4).
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
	} else if err := s.inPlace(ctx, cmd.Request); err != nil {
		return domain.Question{}, err
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

// inPlace is nil when the request may edit the question outside any context:
// its owner and scope.all. A reader who may not is refused as Update says, and
// a question the request cannot read is ErrNotFound.
func (s UpdateHandler) inPlace(ctx context.Context, req domain.WriteRequest) error {
	if req.All {
		return nil
	}
	level, err := s.Repo.Access(ctx, req.Scope(), req.ID)
	switch {
	case err != nil:
		return err
	case level == domain.AccessEdit:
		return domain.ErrEditContextRequired
	case level == domain.AccessUse:
		return domain.ErrReadOnly
	}
	return nil
}
