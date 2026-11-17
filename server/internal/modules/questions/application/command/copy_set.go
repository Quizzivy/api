package command

import (
	"context"

	"quizzivy/internal/modules/questions/application/internal/support"
	"quizzivy/internal/modules/questions/domain"
)

// CopySet is "Make a copy" of a question set (copyQuestionSet, D4, T-R7.7),
// for anyone who can read it: without QuestionIDs the whole set becomes a new
// set of the actor's with copies of its questions; with them, each a member
// of the set, just those are copied into the actor's bank ("Add to test" from
// a shared set). A copy is independent of its source.
type CopySet struct {
	Write       domain.SetWrite
	QuestionIDs []string
}

type CopySetHandler struct {
	*support.Service
}

func (s CopySetHandler) Handle(ctx context.Context, cmd CopySet) (domain.SetCopy, error) {
	if cmd.QuestionIDs != nil {
		if err := domain.SetQuestionIDs(cmd.QuestionIDs); err != nil {
			return domain.SetCopy{}, err
		}
	}
	cmd.Write.Now = s.Now()
	return s.Sets.CopySet(ctx, cmd.Write, cmd.QuestionIDs)
}
