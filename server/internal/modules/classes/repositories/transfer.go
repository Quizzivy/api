package repositories

import (
	"context"
	"fmt"

	"quizzivy/internal/shared/ties"
)

// TransferOwnership implements domain.OwnershipTransfer. The new teacher is
// recorded as teaching every member of the classes moved (T-R7.9).
func (s *Postgres) TransferOwnership(ctx context.Context, from, to string) (int, error) {
	var moved []string
	if err := s.QueryRow(ctx, `
	WITH locked AS (
		SELECT x.id FROM app.classes x WHERE x.teacher_id = $1::uuid ORDER BY x.id FOR NO KEY UPDATE
	), moved AS (
		UPDATE app.classes x SET teacher_id = $2::uuid FROM locked WHERE x.id = locked.id RETURNING x.id
	)
	SELECT coalesce(array_agg(id::text), '{}') FROM moved`, from, to).Scan(&moved); err != nil {
		return 0, fmt.Errorf("classes: transfer ownership: %w", err)
	}
	if err := ties.Reassigned(ctx, s, moved); err != nil {
		return 0, fmt.Errorf("classes: transfer ownership: %w", err)
	}
	return len(moved), nil
}
