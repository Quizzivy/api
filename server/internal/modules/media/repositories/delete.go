package repositories

import (
	"context"
	"errors"
	"fmt"
	"quizzivy/internal/modules/media/domain"
	"quizzivy/internal/platform/db"
	"quizzivy/internal/shared/audit"
	"quizzivy/internal/shared/opt"
	"strings"

	"github.com/jackc/pgx/v5"
)

const scopedAsset = `($2::boolean OR owner_id = $3::uuid)`

// SoftDelete marks an unreferenced asset in the actor's scope deleted and
// audits it. The scope is checked before the references, so an asset outside
// it answers ErrNotFound and never the tests and groups that use it. The
// asset's owner's own published versions and groups refuse it as
// ReferencedError, naming them, as before; then any content of another owner
// that uses it (a copy or a share's, T-R7.7) refuses it as InUseElsewhereError,
// counted and never named. Both are read under the asset's row lock, which a
// copy takes before it names the asset, so neither can start using it before
// this commits. It answers the file as it left the library, with the time its
// object goes: seven days on when nothing names it, and none while a live
// question still does.
func (s *Postgres) SoftDelete(ctx context.Context, in domain.DeleteInput) (domain.Retired, error) {
	tx, err := s.Begin(ctx)
	if err != nil {
		return domain.Retired{}, fmt.Errorf("media: begin delete: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var gone bool
	var owner string
	err = tx.QueryRow(ctx,
		`SELECT deleted_at IS NOT NULL OR purged_at IS NOT NULL, owner_id::text FROM app.media_assets WHERE id = $1 AND `+scopedAsset+` FOR UPDATE`,
		in.ID, in.All, opt.String(in.ActorID)).Scan(&gone, &owner)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Retired{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Retired{}, fmt.Errorf("media: lock asset: %w", err)
	}
	if gone {
		return domain.Retired{}, domain.ErrNotFound
	}
	refs, err := deleteBlockers(ctx, tx, in.ID, owner)
	if err != nil {
		return domain.Retired{}, err
	}

	asset, err := scanAsset(tx.QueryRow(ctx,
		`UPDATE app.media_assets AS a SET deleted_at = $2 WHERE a.id = $1 RETURNING `+assetColumns,
		in.ID, in.Now))
	if err != nil {
		return domain.Retired{}, fmt.Errorf("media: soft delete: %w", err)
	}
	retired, err := describeRetired(ctx, tx, asset, refs, in.Now)
	if err != nil {
		return domain.Retired{}, err
	}

	if err := audit.Write(ctx, tx, audit.Entry{
		ActorUserID: &in.ActorID,
		Action:      "media.deleted",
		Entity:      "media_asset",
		EntityID:    &in.ID,
		OccurredAt:  in.Now,
		IP:          opt.String(in.IP),
		UserAgent:   opt.String(in.UserAgent),
	}); err != nil {
		return domain.Retired{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Retired{}, fmt.Errorf("media: commit delete: %w", err)
	}
	return retired, nil
}

// deleteBlockers is what refuses the deletion of the asset assetID, owned by
// owner: ReferencedError naming owner's published versions and groups that
// use it, then InUseElsewhereError counting what of other owners' uses it.
// It answers owner's versions, none when nothing refuses.
func deleteBlockers(ctx context.Context, tx pgx.Tx, assetID, owner string) ([]domain.TestRef, error) {
	byAsset, err := referencesFor(ctx, tx, []string{assetID}, &owner)
	if err != nil {
		return nil, err
	}
	refs := byAsset[strings.ToLower(assetID)]
	groups, err := groupReferences(ctx, tx, assetID, &owner)
	if err != nil {
		return nil, err
	}
	if len(refs) > 0 || len(groups) > 0 {
		return nil, &domain.ReferencedError{Tests: refs, Groups: groups}
	}
	elsewhere, err := usesElsewhere(ctx, tx, assetID, owner)
	if err != nil {
		return nil, err
	}
	if elsewhere > 0 {
		return nil, &domain.InUseElsewhereError{Count: elsewhere}
	}
	return refs, nil
}

// usesElsewhere counts the content of owners other than owner that uses the
// asset assetID: live standalone bank questions that attach it or show it in
// their prose, groups whose owner is another (bank groups and section groups
// alike, as GroupReferences counts them), and published versions of another
// owner's tests (as References counts them).
func usesElsewhere(ctx context.Context, q db.Querier, assetID, owner string) (int, error) {
	var n int
	err := q.QueryRow(ctx, `SELECT
		  (SELECT count(*) FROM app.questions q
		    WHERE q.context_group_id IS NULL AND q.deleted_at IS NULL AND q.owner_id IS DISTINCT FROM $2::uuid
		      AND (q.media_asset_id = $1
		        OR EXISTS (SELECT 1 FROM app.question_media_references r WHERE r.question_id = q.id AND r.media_asset_id = $1)))
		+ (SELECT count(*) FROM app.question_groups g
		     LEFT JOIN app.test_sections s ON s.id = g.owner_section_id
		     LEFT JOIN app.tests t ON t.id = s.test_id
		    WHERE g.id IN (`+referencedGroups+`) AND `+groupOwner+` IS DISTINCT FROM $2::uuid)`,
		assetID, owner).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("media: uses elsewhere: %w", err)
	}
	all, err := References(ctx, q, assetID)
	if err != nil {
		return 0, err
	}
	byAsset, err := referencesFor(ctx, q, []string{assetID}, &owner)
	if err != nil {
		return 0, err
	}
	return n + len(all) - len(byAsset[strings.ToLower(assetID)]), nil
}
