package command

import (
	"context"
	"quizzivy/internal/modules/questions/application/internal/support"
	"quizzivy/internal/modules/questions/domain"
)

// Update edits a bank question. With SetID the edit is made in that set's
// context, which the actor must be able to edit and which must hold the
// question, under the copy-on-write rule (domain.QuestionUse.Forks): a
// non-owner's edit of a question something else uses makes a copy that only
// this set is pointed at, and the answer is the copy. Without it, the
// question's owner and scope.all edit it in place. Anyone else who reads it,
// through a test or a set shared with them, gets ErrReadOnly when they may
// only use it and ErrEditContextRequired when they may edit it, since such an
// edit is made in its test's or set's context (D4).
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
	if cmd.SetID != "" && s.Contexts == nil {
		return domain.Question{}, domain.ErrSetNotFound
	}
	if cmd.SetID == "" {
		if err := s.inPlace(ctx, cmd.Request); err != nil {
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
	if cmd.SetID != "" {
		return s.Contexts.UpdateInSet(ctx, s.InputOf(cmd.Request), cmd.SetID)
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
