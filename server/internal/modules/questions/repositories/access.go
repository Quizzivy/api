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

// questionAccess is this module's predicate for a bank question q (D4,
// T-R7.4), as the contract's AdminQuestion.access reads: 'owner' for its owner
// and for scope.all; 'edit' when the draft of a live test, or a live set, that
// the caller can edit holds it; 'use' when only one the caller can read does;
// NULL for anyone else. A question is never shared on its own: it is read
// through the test or set that holds it, so the grants are those of the
// caller's on the tests whose draft names it and on the sets that hold it.
// all and user are the placeholders of the caller's scope.all and user id.
// Every read of a bank question by id decides access here.
func questionAccess(all, user int) string {
	return fmt.Sprintf(`(CASE WHEN $%[1]d::boolean OR q.owner_id = $%[2]d::uuid THEN 'owner'
	  ELSE (SELECT CASE WHEN count(*) = 0 THEN NULL WHEN bool_or(g.access = 'edit') THEN 'edit' ELSE 'use' END
	          FROM app.content_shares g
	         WHERE g.grantee_id = $%[2]d::uuid
	           AND (g.test_id IN (SELECT section.test_id FROM app.test_section_questions sq
	                                JOIN app.test_sections section ON section.id = sq.test_section_id
	                                JOIN app.tests t ON t.id = section.test_id AND t.deleted_at IS NULL
	                               WHERE sq.question_id = q.id)
	             OR g.question_set_id IN (SELECT i.set_id FROM app.question_set_items i
	                                        JOIN app.question_sets st ON st.id = i.set_id AND st.deleted_at IS NULL
	                                       WHERE i.question_id = q.id))) END)`, all, user)
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
