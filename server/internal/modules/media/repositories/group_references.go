package repositories

import (
	"context"
	"quizzivy/internal/modules/media/domain"
	"quizzivy/internal/platform/db"

	"github.com/jackc/pgx/v5"
)

// referencedGroups is the groups that use the asset in $1: its stimulus
// assets and recordings, and the media and prose of its member questions.
const referencedGroups = `
		SELECT group_id FROM app.group_stimulus_assets WHERE media_asset_id=$1
		UNION
		SELECT group_id FROM app.group_recordings WHERE media_asset_id=$1
		UNION
		SELECT context_group_id FROM app.questions WHERE media_asset_id=$1 AND context_group_id IS NOT NULL
		UNION
		SELECT q.context_group_id FROM app.question_media_references r JOIN app.questions q ON q.id=r.question_id
		 WHERE r.media_asset_id=$1 AND q.context_group_id IS NOT NULL`

// groupOwner is the owner of the group g: a bank group's own, a section
// group's test's (t, joined through the section s).
const groupOwner = `CASE WHEN g.owner_section_id IS NULL THEN g.owner_id ELSE t.owner_id END`

// GroupReferences names every independent context protecting an asset, including archived groups and member-only media.
func GroupReferences(ctx context.Context, q db.Querier, assetID string) ([]domain.GroupRef, error) {
	return groupReferences(ctx, q, assetID, nil)
}

// groupReferences is GroupReferences, kept to the groups ownerID owns when
// ownerID is not nil: a bank group by its owner, a section group by its
// test's.
func groupReferences(ctx context.Context, q db.Querier, assetID string, ownerID *string) ([]domain.GroupRef, error) {
	rows, err := q.Query(ctx, `WITH referenced_groups AS (`+referencedGroups+`
	)
	SELECT g.id::text,g.title,s.test_id::text
	FROM referenced_groups r JOIN app.question_groups g ON g.id=r.group_id
	LEFT JOIN app.test_sections s ON s.id=g.owner_section_id
	LEFT JOIN app.tests t ON t.id=s.test_id
	WHERE $2::uuid IS NULL OR `+groupOwner+`=$2::uuid
	ORDER BY g.title,g.id`, assetID, ownerID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[domain.GroupRef])
}
