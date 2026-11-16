package repositories

import (
	"context"
	"fmt"
	"quizzivy/internal/modules/questions/domain"
)

// TransferOwnership implements domain.OwnershipTransfer. The sets are locked
// and moved before the questions: every write in a set's context (UpdateInSet,
// CopySet, AddToSet, a repoint) locks the set and then its question, and a
// transfer can run while a disabled owner's grantees still work in their sets.
func (s *Postgres) TransferOwnership(ctx context.Context, from, to string) (domain.Transferred, error) {
	var moved domain.Transferred
	if err := s.QueryRow(ctx, `
	WITH locked AS (
		SELECT x.id FROM app.question_sets x WHERE x.owner_id = $1::uuid ORDER BY x.id FOR NO KEY UPDATE
	), moved AS (
		UPDATE app.question_sets x SET owner_id = $2::uuid, updated_by = NULL FROM locked WHERE x.id = locked.id RETURNING x.deleted_at IS NULL AS live
	)
	SELECT count(*) FILTER (WHERE live)::int FROM moved`, from, to).Scan(&moved.Sets); err != nil {
		return domain.Transferred{}, fmt.Errorf("questions: transfer question set ownership: %w", err)
	}
	if err := s.QueryRow(ctx, `
	WITH locked AS (
		SELECT x.id FROM app.questions x WHERE x.owner_id = $1::uuid ORDER BY x.id FOR NO KEY UPDATE
	), moved AS (
		UPDATE app.questions x SET owner_id = $2::uuid, updated_by = NULL FROM locked WHERE x.id = locked.id RETURNING x.deleted_at IS NULL AS live
	)
	SELECT count(*) FILTER (WHERE live)::int FROM moved`, from, to).Scan(&moved.Questions); err != nil {
		return domain.Transferred{}, fmt.Errorf("questions: transfer ownership: %w", err)
	}
	return moved, nil
}
