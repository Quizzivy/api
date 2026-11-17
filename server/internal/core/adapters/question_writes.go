package adapters

import (
	"context"

	"quizzivy/internal/modules/questions/domain"
	"quizzivy/internal/modules/questions/repositories"
	"quizzivy/internal/platform/db"

	"github.com/jackc/pgx/v5"
)

// QuestionWrites binds the questions module's copy-on-write calls to the tests
// module's transaction (editTestQuestion, T-R7.6): the question's use read
// under its row lock, and the edit written in place or as a fork with the
// question store's media locks; and its copies (copyTest and
// copyQuestionsForTest, T-R7.7), with the same media locks.
type QuestionWrites struct{}

func (QuestionWrites) ContextUse(ctx context.Context, tx pgx.Tx, questionID string, within domain.EditContext) (domain.QuestionUse, error) {
	return repositories.ContextUse(ctx, tx, questionID, within)
}

func (QuestionWrites) WriteInContext(ctx context.Context, tx pgx.Tx, in domain.WriteInput, use domain.QuestionUse, within domain.EditContext) (domain.Question, bool, error) {
	return repositories.NewPostgres(db.NewContext(tx)).WithMedia(QuestionMedia{}).WriteInContext(ctx, in, use, within)
}

func (QuestionWrites) CopyQuestions(ctx context.Context, tx pgx.Tx, in domain.Copy) ([]string, error) {
	return repositories.CopyQuestions(ctx, tx, QuestionMedia{}, in)
}
