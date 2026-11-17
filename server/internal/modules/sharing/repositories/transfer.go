package repositories

import (
	"context"
	"fmt"

	"quizzivy/internal/modules/sharing/domain"
	"quizzivy/internal/shared/opt"
)

var _ domain.OwnershipTransfer = (*Postgres)(nil)

// DropOwnGrants implements domain.OwnershipTransfer. It runs after the tests
// and sets moved, whose rows the transfer holds FOR NO KEY UPDATE, so a share
// of one of them (which takes its row FOR SHARE) either committed before and
// is seen here, or waits and then finds the new owner, whom it skips as SELF.
func (p *Postgres) DropOwnGrants(ctx context.Context, w domain.Write, ownerID string) (int, error) {
	var dropped int
	err := p.QueryRow(ctx, `
		WITH gone AS (
		  DELETE FROM app.content_shares g
		   WHERE g.grantee_id = $1::uuid
		     AND (g.test_id IN (SELECT t.id FROM app.tests t WHERE t.owner_id = $1::uuid)
		       OR g.question_set_id IN (SELECT s.id FROM app.question_sets s WHERE s.owner_id = $1::uuid))
		  RETURNING g.kind, coalesce(g.test_id, g.question_set_id) AS resource_id, g.grantee_id, g.access
		), logged AS (
		  INSERT INTO app.audit_log (actor_user_id, action, entity, entity_id, occurred_at, ip, user_agent, diff)
		  SELECT $2::uuid, 'share.revoked', gone.kind, gone.resource_id, $3, $4::inet, $5,
		         jsonb_build_object('shared_with', jsonb_build_object(
		           'old', jsonb_build_object('granteeId', gone.grantee_id, 'access', gone.access), 'new', NULL))
		    FROM gone
		)
		SELECT count(*)::int FROM gone`,
		ownerID, w.ActorID, w.Now, opt.String(w.IP), opt.String(w.UserAgent)).Scan(&dropped)
	if err != nil {
		return 0, fmt.Errorf("sharing: drop the new owner's grants: %w", err)
	}
	return dropped, nil
}
