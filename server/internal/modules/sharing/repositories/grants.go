package repositories

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5"

	"quizzivy/internal/modules/sharing/domain"
	"quizzivy/internal/platform/db"
	"quizzivy/internal/shared/limits"
	"quizzivy/internal/shared/opt"
)

// Grant shares the live resource, under a share lock on its row so a delete
// waits for it, with each recipient domain.SkipReason lets through, and audits each
// grant in the statement that inserts it. A recipient whose grant another
// share inserted meanwhile is skipped as ALREADY_SHARED.
func (p *Postgres) Grant(ctx context.Context, req domain.GrantRequest) (domain.Granted, error) {
	tx, err := p.Begin(ctx)
	if err != nil {
		return domain.Granted{}, fmt.Errorf("sharing: begin grant: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	candidates, owner, err := lockCandidates(ctx, tx, req)
	if err != nil {
		return domain.Granted{}, err
	}
	out := domain.Granted{Grants: []domain.Grant{}, Skipped: []domain.Skipped{}}
	var granting []string
	for _, c := range candidates {
		if code, skipped := domain.SkipReason(c, owner, req.ActorID); skipped {
			out.Skipped = append(out.Skipped, domain.Skipped{UserID: c.UserID, Code: code})
			continue
		}
		granting = append(granting, c.UserID)
	}
	if len(granting) > 0 {
		grants, err := insertGrants(ctx, tx, req, granting)
		if err != nil {
			return domain.Granted{}, err
		}
		for _, id := range granting {
			at := slices.IndexFunc(grants, func(g domain.Grant) bool { return g.Grantee.ID == id })
			if at < 0 {
				out.Skipped = append(out.Skipped, domain.Skipped{UserID: id, Code: domain.SkipAlreadyShared})
				continue
			}
			out.Grants = append(out.Grants, grants[at])
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Granted{}, fmt.Errorf("sharing: commit grant: %w", err)
	}
	return out, nil
}

func lockCandidates(ctx context.Context, tx pgx.Tx, req domain.GrantRequest) ([]domain.Candidate, string, error) {
	r := tableOf(req.Resource.Kind)
	var owner string
	err := tx.QueryRow(ctx, `SELECT owner_id::text FROM `+r.table+` WHERE id = $1::uuid AND deleted_at IS NULL FOR SHARE`, req.Resource.ID).Scan(&owner)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", domain.ErrNotFound
	}
	if err != nil {
		return nil, "", fmt.Errorf("sharing: lock resource: %w", err)
	}
	candidates, err := db.QueryMany(ctx, tx, `
		SELECT c.id::text, u.id IS NOT NULL, coalesce(`+limits.TeacherRole("u.role_id")+`, false), coalesce(u.disabled_at IS NOT NULL, false),
		       EXISTS (SELECT 1 FROM app.content_shares g WHERE g.`+r.column+` = $1::uuid AND g.grantee_id = c.id)
		  FROM unnest($2::uuid[]) WITH ORDINALITY AS c(id, n)
		  LEFT JOIN app.users u ON u.id = c.id
		 ORDER BY c.n`, []any{req.Resource.ID, req.GranteeIDs}, func(rows pgx.Rows) (domain.Candidate, error) {
		var c domain.Candidate
		return c, rows.Scan(&c.UserID, &c.Exists, &c.Teacher, &c.Disabled, &c.Granted)
	})
	if err != nil {
		return nil, "", fmt.Errorf("sharing: recipients: %w", err)
	}
	return candidates, owner, nil
}

func insertGrants(ctx context.Context, tx pgx.Tx, req domain.GrantRequest, granteeIDs []string) ([]domain.Grant, error) {
	rows, err := tx.Query(ctx, `
		WITH granted AS (
		  INSERT INTO app.content_shares (kind, test_id, question_set_id, grantee_id, access, granted_by, message)
		  SELECT $1::text, CASE WHEN $1::text = 'test' THEN $2::uuid END, CASE WHEN $1::text = 'question_set' THEN $2::uuid END,
		         g.id, $4::text, $5::uuid, $6::text
		    FROM unnest($3::uuid[]) WITH ORDINALITY AS g(id, n)
		   ORDER BY g.n
		  ON CONFLICT DO NOTHING
		  RETURNING id, grantee_id, access
		), logged AS (
		  INSERT INTO app.audit_log (actor_user_id, action, entity, entity_id, occurred_at, ip, user_agent, diff)
		  SELECT $5::uuid, 'share.granted', $1::text, $2::uuid, $7, $8::inet, $9,
		         jsonb_build_object('shared_with', jsonb_build_object('old', NULL,
		           'new', jsonb_build_object('granteeId', granted.grantee_id, 'access', granted.access)))
		    FROM granted
		)
		SELECT id::text FROM granted`,
		string(req.Resource.Kind), req.Resource.ID, granteeIDs, string(req.Access), req.ActorID, req.Message,
		req.Now, opt.String(req.IP), opt.String(req.UserAgent))
	if err != nil {
		return nil, fmt.Errorf("sharing: insert grants: %w", err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("sharing: insert grants: %w", err)
	}
	return grantsByID(ctx, tx, ids)
}

// ChangeAccess sets the grant to access and audits the change in the same
// statement; a grant already at access is left as it is, with no row. The
// grant must still be the one share names.
func (p *Postgres) ChangeAccess(ctx context.Context, w domain.Write, share domain.Share, access domain.Access) (domain.Grant, error) {
	tx, err := p.Begin(ctx)
	if err != nil {
		return domain.Grant{}, fmt.Errorf("sharing: begin change: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `
		WITH changed AS (
		  UPDATE app.content_shares g SET access = $2::text
		   WHERE g.id = $1::uuid AND g.grantee_id = $3::uuid AND g.access <> $2::text
		  RETURNING g.kind, coalesce(g.test_id, g.question_set_id) AS resource_id, g.grantee_id,
		            old.access AS old_access, new.access AS new_access
		)
		INSERT INTO app.audit_log (actor_user_id, action, entity, entity_id, occurred_at, ip, user_agent, diff)
		SELECT $4::uuid, 'share.changed', changed.kind, changed.resource_id, $5, $6::inet, $7,
		       jsonb_build_object('shared_with', jsonb_build_object(
		         'old', jsonb_build_object('granteeId', changed.grantee_id, 'access', changed.old_access),
		         'new', jsonb_build_object('granteeId', changed.grantee_id, 'access', changed.new_access)))
		  FROM changed`,
		share.ID, string(access), share.GranteeID, w.ActorID, w.Now, opt.String(w.IP), opt.String(w.UserAgent)); err != nil {
		return domain.Grant{}, fmt.Errorf("sharing: change access: %w", err)
	}
	grants, err := grantsByID(ctx, tx, []string{share.ID})
	if err != nil {
		return domain.Grant{}, err
	}
	if len(grants) == 0 || grants[0].Grantee.ID != share.GranteeID {
		return domain.Grant{}, domain.ErrNotFound
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Grant{}, fmt.Errorf("sharing: commit change: %w", err)
	}
	return grants[0], nil
}

// Revoke deletes the grant for the resource's owner or scope.all (Stop
// sharing) and audits it as share.revoked, in one statement.
func (p *Postgres) Revoke(ctx context.Context, w domain.Write, share domain.Share) error {
	return p.delete(ctx, w, share, false)
}

// Remove deletes the grant for its grantee (Remove from my list) and audits it
// as share.removed, in one statement.
func (p *Postgres) Remove(ctx context.Context, w domain.Write, share domain.Share) error {
	return p.delete(ctx, w, share, true)
}

func (p *Postgres) delete(ctx context.Context, w domain.Write, share domain.Share, byGrantee bool) error {
	tag, err := p.Exec(ctx, `
		WITH gone AS (
		  DELETE FROM app.content_shares g
		   WHERE g.id = $1::uuid AND g.grantee_id = $2::uuid
		  RETURNING g.kind, coalesce(g.test_id, g.question_set_id) AS resource_id, g.grantee_id, g.access
		)
		INSERT INTO app.audit_log (actor_user_id, action, entity, entity_id, occurred_at, ip, user_agent, diff)
		SELECT $3::uuid, CASE WHEN $4::boolean THEN 'share.removed' ELSE 'share.revoked' END, gone.kind, gone.resource_id, $5, $6::inet, $7,
		       jsonb_build_object('shared_with', jsonb_build_object(
		         'old', jsonb_build_object('granteeId', gone.grantee_id, 'access', gone.access), 'new', NULL))
		  FROM gone`,
		share.ID, share.GranteeID, w.ActorID, byGrantee, w.Now, opt.String(w.IP), opt.String(w.UserAgent))
	if err != nil {
		return fmt.Errorf("sharing: delete grant: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}
