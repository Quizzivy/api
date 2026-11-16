package command

import (
	"context"

	"quizzivy/internal/modules/questions/application/internal/support"
	"quizzivy/internal/modules/questions/domain"
)

// UpdateSet renames a question set or changes its description, for its owner
// or an edit grantee.
type UpdateSet struct {
	Write  domain.SetWrite
	Fields domain.SetFields
}

type UpdateSetHandler struct {
	*support.Service
}

func (s UpdateSetHandler) Handle(ctx context.Context, cmd UpdateSet) (domain.QuestionSet, error) {
	fields, err := cmd.Fields.Clean()
	if err != nil {
		return domain.QuestionSet{}, err
	}
	cmd.Write.Now = s.Now()
	return s.Sets.UpdateSet(ctx, cmd.Write, fields)
}
