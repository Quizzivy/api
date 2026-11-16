package repositories

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"quizzivy/internal/modules/questions/domain"
	"quizzivy/internal/shared/access"
	"quizzivy/internal/shared/opt"
	"quizzivy/internal/shared/visibility"
)

// SetAccess is this module's predicate for a question set (D4): the SQL
// expression for what the caller may do with the set s, 'owner' for its owner
// and for scope.all, the access of the caller's grant in app.content_shares
// ('edit' or 'use'), and NULL for anyone who cannot read it. all and user are
// the placeholders of the caller's scope.all and user id. Every read and
// write of a set decides access here and nowhere else.
func SetAccess(all, user int) string {
	return visibility.SetAccess("s", all, user)
}

// questionAccess is this module's predicate for a bank question q, as the
// contract's AdminQuestion.access reads it: visibility.QuestionAccess over q,
// the one definition the media module's readable assets share. all and user
// are the placeholders of the caller's scope.all and user id. Every read of a
// bank question by id decides access here.
func questionAccess(all, user int) string {
	return visibility.QuestionAccess("q", all, user)
}

// readableTest is the condition, over the tests row t, that the caller may
// read it, under the tests module's own rule (visibility.TestAccess): the
// tests a question's usage lists.
func readableTest(all, user int) string {
	return visibility.TestAccess("t", all, user) + ` IS NOT NULL`
}

// Access is what scope may do with the live standalone bank question id
// (questionAccess); a question it cannot read is domain.ErrNotFound, as a
// missing one is.
func (s *Postgres) Access(ctx context.Context, scope access.Scope, id string) (domain.Access, error) {
	var level *string
	err := s.QueryRow(ctx, `SELECT `+questionAccess(2, 3)+` FROM app.questions q
		 WHERE q.id = $1::uuid AND q.context_group_id IS NULL AND q.deleted_at IS NULL`,
		id, scope.All, opt.String(scope.UserID)).Scan(&level)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && level == nil {
		return "", domain.ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("questions: access: %w", err)
	}
	return domain.Access(*level), nil
}
