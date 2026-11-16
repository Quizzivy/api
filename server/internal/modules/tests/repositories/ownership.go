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
// read it, under the module's read rule; any other test is domain.ErrNotFound.
func (s *Postgres) Ownership(ctx context.Context, scope access.Scope, id string) (domain.Ownership, error) {
	var o domain.Ownership
	err := s.QueryRow(ctx, `SELECT id::text, title, owner_id::text FROM app.tests
		 WHERE id = $1::uuid AND deleted_at IS NULL AND `+scopedTest,
		id, scope.All, opt.String(scope.UserID)).Scan(&o.TestID, &o.Title, &o.OwnerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Ownership{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Ownership{}, fmt.Errorf("tests: ownership: %w", err)
	}
	return o, nil
}
