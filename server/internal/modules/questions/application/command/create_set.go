package command

import (
	"context"

	"quizzivy/internal/modules/questions/application/internal/support"
	"quizzivy/internal/modules/questions/domain"
)

// CreateSet makes a question set from the bank's selection: a name, an
// optional description and the questions in the order given, each a live
// standalone question of the actor's bank.
type CreateSet struct {
	Write       domain.SetWrite
	Name        string
	Description *string
	QuestionIDs []string
}

type CreateSetHandler struct {
	*support.Service
}

func (s CreateSetHandler) Handle(ctx context.Context, cmd CreateSet) (domain.QuestionSet, error) {
	fields, err := domain.SetFields{Name: &cmd.Name, Description: cmd.Description}.Clean()
	if err != nil {
		return domain.QuestionSet{}, err
	}
	if err := domain.SetQuestionIDs(cmd.QuestionIDs); err != nil {
		return domain.QuestionSet{}, err
	}
	cmd.Write.Now = s.Now()
	return s.Sets.CreateSet(ctx, cmd.Write, fields, cmd.QuestionIDs)
}
