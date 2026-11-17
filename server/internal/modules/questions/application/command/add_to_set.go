package command

import (
	"context"

	"quizzivy/internal/modules/questions/application/internal/support"
	"quizzivy/internal/modules/questions/domain"
)

// AddToSet appends bank questions to a question set, for its owner; one the
// set already holds is ignored.
type AddToSet struct {
	Write       domain.SetWrite
	QuestionIDs []string
}

type AddToSetHandler struct {
	*support.Service
}

func (s AddToSetHandler) Handle(ctx context.Context, cmd AddToSet) (domain.QuestionSet, error) {
	if err := domain.SetQuestionIDs(cmd.QuestionIDs); err != nil {
		return domain.QuestionSet{}, err
	}
	cmd.Write.Now = s.Now()
	return s.Sets.AddToSet(ctx, cmd.Write, cmd.QuestionIDs)
}
