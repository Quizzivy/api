package repositories

import (
	"context"
	"errors"
	"quizzivy/internal/modules/tests/domain"
	"quizzivy/internal/shared/audit"
	"quizzivy/internal/shared/opt"
	"quizzivy/internal/shared/provenance"

	"github.com/jackc/pgx/v5"
)

// lockGroupMutation locks group in.ID for a write by in.Scope: a test-owned
// group's test for an edit first, then the group. A test or a group past the
// revision the caller read is domain.ErrStaleWrite naming the last write the
// caller had not seen (staleGroup).
func lockGroupMutation(ctx context.Context, tx pgx.Tx, in domain.GroupMutation) (domain.StoredGroup, string, error) {
	var testID *string
	err := tx.QueryRow(ctx, `SELECT s.test_id::text FROM app.question_groups g
		LEFT JOIN app.test_sections s ON s.id=g.owner_section_id WHERE g.id=$1`, in.ID).Scan(&testID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.StoredGroup{}, "", domain.ErrNotFound
	}
	if err != nil {
		return domain.StoredGroup{}, "", err
	}
	var reached *reachedTest
	if testID != nil {
		locked, err := checkVersion(ctx, tx, *testID, in.ExpectedTestUpdatedAt, in.Scope)
		if err != nil {
			return domain.StoredGroup{}, "", err
		}
		reached = &locked
		var archived bool
		if err := tx.QueryRow(ctx, `SELECT status='archived' FROM app.tests WHERE id=$1`, *testID).Scan(&archived); err != nil {
			return domain.StoredGroup{}, "", err
		}
		if archived {
			return domain.StoredGroup{}, "", &domain.GroupError{Rule: "group_owner_archived"}
		}
	}
	stored, err := readLockedGroup(ctx, tx, in.ID, groupExclusive)
	if err != nil {
		return domain.StoredGroup{}, "", err
	}
	if !reachableGroup(in.Scope, stored) {
		return domain.StoredGroup{}, "", domain.ErrNotFound
	}
	if err := domain.RequireGroupWrite(in.Grants, stored.OwnerSectionID == nil); err != nil {
		return domain.StoredGroup{}, "", err
	}
	if stored.Revision != in.ExpectedRevision {
		return domain.StoredGroup{}, "", staleGroup(stored, reached)
	}
	if testID == nil {
		return stored, "", nil
	}
	return stored, *testID, nil
}

// staleGroup is domain.ErrStaleWrite for stored, a group past the revision
// the caller read. A test-owned group names its test's last write, reached
// under the lock this write holds, since every write of the group records
// itself on its test. A bank group, or a copy's source, whose test this write
// does not lock, names no one and the time the group changed.
func staleGroup(stored domain.StoredGroup, reached *reachedTest) error {
	last := provenance.LastWrite{At: stored.UpdatedAt}
	if reached != nil {
		last = reached.last
	}
	return provenance.Stale(domain.ErrStaleWrite, last)
}

func lockGroupMembers(ctx context.Context, tx pgx.Tx, id string) ([]string, error) {
	rows, err := tx.Query(ctx, `SELECT id::text FROM app.questions WHERE context_group_id=$1 ORDER BY id FOR UPDATE`, id)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

func recordGroupMutation(ctx context.Context, tx pgx.Tx, in domain.GroupMutation, testID, action string) error {
	if testID != "" {
		if _, err := tx.Exec(ctx, `UPDATE app.tests SET updated_at=$2, updated_by=$3 WHERE id=$1`, testID, in.Now, in.ActorID); err != nil {
			return err
		}
	}
	return audit.Write(ctx, tx, audit.Entry{ActorUserID: &in.ActorID, Action: action, Entity: "question_group", EntityID: &in.ID, OccurredAt: in.Now, IP: opt.String(in.IP), UserAgent: opt.String(in.UserAgent)})
}

func clearGroupMaterials(ctx context.Context, tx pgx.Tx, id string) error {
	if _, err := tx.Exec(ctx, `DELETE FROM app.group_stimuli WHERE group_id=$1`, id); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `DELETE FROM app.group_recordings WHERE group_id=$1`, id)
	return err
}
