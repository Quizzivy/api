package command

import (
	"context"

	"quizzivy/internal/modules/tests/application/internal/support"
	"quizzivy/internal/modules/tests/domain"
)

// CopyQuestions is copyQuestionsForTest (T-R7.7): copies of questions the
// actor can read, made in the bank of the owner of a test the actor can edit,
// which the builder uses when the actor is not the test's owner.
type CopyQuestions struct {
	Copies domain.QuestionCopies
}

type CopyQuestionsHandler struct {
	*support.Service
}

func (s CopyQuestionsHandler) Handle(ctx context.Context, cmd CopyQuestions) ([]string, error) {
	copies := cmd.Copies
	if copies.ID == "" {
		return nil, domain.ErrNotFound
	}
	copies.Now = s.Now()
	return s.Repo.CopyQuestions(ctx, copies)
}
