package command

import (
	"context"

	"quizzivy/internal/modules/questions/application/internal/support"
	"quizzivy/internal/modules/questions/domain"
)

// RemoveFromSet takes questions out of a question set, for its owner or an
// edit grantee; one the set does not hold is ignored.
type RemoveFromSet struct {
	Write       domain.SetWrite
	QuestionIDs []string
}

type RemoveFromSetHandler struct {
	*support.Service
}

func (s RemoveFromSetHandler) Handle(ctx context.Context, cmd RemoveFromSet) (domain.QuestionSet, error) {
	if err := domain.SetQuestionIDs(cmd.QuestionIDs); err != nil {
		return domain.QuestionSet{}, err
	}
	cmd.Write.Now = s.Now()
	return s.Sets.RemoveFromSet(ctx, cmd.Write, cmd.QuestionIDs)
}
