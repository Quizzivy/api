package command

import (
	"context"

	"quizzivy/internal/modules/tests/application/internal/support"
	"quizzivy/internal/modules/tests/domain"
)

// EditQuestion is editTestQuestion: an edit of a standalone question of the
// test's draft made in the test's context, which is how the builder saves a
// question when the caller is not the test's owner (copy-on-write, D4). The
// input is composed and held to the authoring rules before any lock is
// taken; the questions module validates it against its media in the write.
type EditQuestion struct{ Edit domain.QuestionEdit }

type EditQuestionHandler struct{ *support.Service }

func (s EditQuestionHandler) Handle(ctx context.Context, cmd EditQuestion) (domain.QuestionEdited, error) {
	edit := cmd.Edit
	if edit.ID == "" || edit.QuestionID == "" {
		return domain.QuestionEdited{}, domain.ErrNotFound
	}
	input, err := edit.Input.Composed()
	if err != nil {
		return domain.QuestionEdited{}, err
	}
	if err := input.ValidateAuthoring(); err != nil {
		return domain.QuestionEdited{}, err
	}
	edit.Input, edit.Now = input, s.Now()
	return s.Repo.EditQuestion(ctx, edit)
}
