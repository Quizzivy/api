package repositories

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"quizzivy/internal/modules/tests/domain"
	"quizzivy/internal/shared/access"
	"quizzivy/internal/shared/opt"
)

// Ownership returns the title and owner of the live test id when scope may
// read it, under the module's read rule, with the reader's access; any other
// test is domain.ErrNotFound.
func (s *Postgres) Ownership(ctx context.Context, scope access.Scope, id string) (domain.Ownership, error) {
	var o domain.Ownership
	var level string
	err := s.QueryRow(ctx, `SELECT t.id::text, t.title, t.owner_id::text, `+testAccess("t", 2, 3)+` FROM app.tests t
		 WHERE t.id = $1::uuid AND t.deleted_at IS NULL AND `+readableTests(2, 3),
		id, scope.All, opt.String(scope.UserID)).Scan(&o.TestID, &o.Title, &o.OwnerID, &level)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Ownership{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Ownership{}, fmt.Errorf("tests: ownership: %w", err)
	}
	o.Access = domain.Access(level)
	return o, nil
}
