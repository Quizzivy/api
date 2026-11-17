package repositories

import (
	"context"
	"quizzivy/internal/modules/tests/domain"
	"time"
)

// Delete permanently removes an archived test only when no assignment or
// attempt references it. It is the owner's (D4): a reader through a grant gets
// domain.ErrOwnerOnly.
func (s *Postgres) Delete(ctx context.Context, req domain.Request, now time.Time) error {
	tx, err := s.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	reached, err := reachTest(ctx, tx, req.Scope, req.ID, domain.ActionOwn, testExclusive)
	if err != nil {
		return err
	}
	if reached.status != domain.Archived {
		return domain.ErrNotArchived
	}
	if _, err := tx.Exec(ctx, `DELETE FROM app.test_versions WHERE test_id = $1`, req.ID); err != nil {
		return referenceError(err)
	}
	if err := clearTestGroups(ctx, tx, req.ID); err != nil {
		return referenceError(err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM app.tests WHERE id = $1`, req.ID); err != nil {
		return referenceError(err)
	}
	if err := auditTestChange(ctx, tx, req, now, "test.deleted"); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// DeleteVersion removes an unused snapshot while preserving the current
// default and assigned history. It is the owner's: a Can edit recipient may
// restore a version as the draft and make it the default, but not delete it
// (web#79).
func (s *Postgres) DeleteVersion(ctx context.Context, req domain.VersionRequest, now time.Time) error {
	tx, err := s.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	reached, err := reachTest(ctx, tx, req.Scope, req.ID, domain.ActionOwn, testExclusive)
	if err != nil {
		return err
	}
	if reached.current == req.Version {
		return domain.ErrCurrentVersion
	}
	versionID, err := lockVersion(ctx, tx, req)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM app.test_versions WHERE id = $1`, versionID); err != nil {
		return referenceError(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE app.tests SET updated_at = now(), updated_by = $2 WHERE id = $1`, req.ID, req.ActorID); err != nil {
		return err
	}
	if err := auditVersionChange(ctx, tx, req, now, "test.version_deleted"); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
