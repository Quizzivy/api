package command

import (
	"context"
	"quizzivy/internal/modules/questions/application/internal/support"
	"quizzivy/internal/modules/questions/domain"
)

// Create adds a question to the bank. With ForTestID it is made for that
// test's draft and belongs to the test's owner, with the actor as its author.
type Create struct {
	Request   domain.WriteRequest
	ForTestID string
}

type CreateHandler struct {
	*support.Service
}

func (s CreateHandler) Handle(ctx context.Context, cmd Create) (domain.Question, error) {
	input, err := cmd.Request.Input.Composed()
	if err != nil {
		return domain.Question{}, err
	}
	cmd.Request.Input = input
	if err := cmd.Request.Input.ValidateAuthoring(); err != nil {
		return domain.Question{}, err
	}
	req, err := s.ForTest(ctx, cmd.Request, cmd.ForTestID)
	if err != nil {
		return domain.Question{}, err
	}
	return s.Write(ctx, req, false)
}
