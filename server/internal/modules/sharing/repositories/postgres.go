// Package repositories keeps the sharing context's grants in
// app.content_shares, reads the people they name from app.users, and audits
// each grant, change and removal in the statement that makes it. A grant's
// diff names ids only, {granteeId, access}, because a name frozen in the
// append-only log could never be anonymised (D16).
package repositories

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"quizzivy/internal/modules/sharing/domain"
	"quizzivy/internal/platform/db"
	"quizzivy/internal/shared/limits"
	"quizzivy/internal/shared/opt"
	"quizzivy/internal/shared/paging"
	"quizzivy/internal/shared/retention"
)

// Page-size bounds for Shared with me.
const (
	DefaultLimit = 20
	MaxLimit     = 100
)

// Postgres keeps grants through dbx.
type Postgres struct {
	db.Repository
}

// NewPostgres keeps grants through dbx.
func NewPostgres(dbx db.Context) *Postgres {
	return &Postgres{Repository: db.NewRepository(dbx)}
}

var _ domain.Repository = (*Postgres)(nil)

const displayName = `coalesce(nullif(%[1]s.display_name, ''), %[1]s.full_name)`

func nameOf(alias string) string { return fmt.Sprintf(displayName, alias) }

func folded(expr string) string { return `app.immutable_unaccent(lower(` + expr + `))` }

func contains(expr string, param int) string {
	return fmt.Sprintf(`%s LIKE '%%' || app.immutable_unaccent(lower($%d)) || '%%' ESCAPE '\'`, folded(expr), param)
}

func startsAWord(expr string, param int) string {
	return fmt.Sprintf(`' ' || %s LIKE '%% ' || app.immutable_unaccent(lower($%d)) || '%%' ESCAPE '\'`, folded(expr), param)
}

type resourceTable struct {
	table, column string
}

func tableOf(kind domain.Kind) resourceTable {
	if kind == domain.KindQuestionSet {
		return resourceTable{table: "app.question_sets", column: "question_set_id"}
	}
	return resourceTable{table: "app.tests", column: "test_id"}
}

const personColumns = `%[1]s.id::text, %[1]s.full_name, ` + displayName + `, %[1]s.email, %[1]s.avatar_key, %[1]s.disabled_at IS NOT NULL`

func personOf(alias string) string { return fmt.Sprintf(personColumns, alias) }

func scanPerson(row pgx.Row) (domain.Person, error) {
	var p domain.Person
	err := row.Scan(&p.ID, &p.FullName, &p.DisplayName, &p.Email, &p.AvatarKey, &p.Disabled)
	return p, err
}

// Directory returns at most limit accounts that open the teacher workspace,
// active or disabled, never actorID and never an anonymised one. A query matches the full name, the
// display name or the email through app.immutable_unaccent(lower(…)), so
// "duc" finds "Đức", and a word that starts with it ranks first; without one
// the rows are in name order.
func (p *Postgres) Directory(ctx context.Context, actorID, query string, limit int) ([]domain.Person, error) {
	fields := []string{`u.full_name`, `coalesce(u.display_name, '')`, `u.email`}
	matches, starts := make([]string, len(fields)), make([]string, len(fields))
	for i, field := range fields {
		matches[i], starts[i] = contains(field, 2), startsAWord(field, 2)
	}
	people, err := db.QueryMany(ctx, p, `SELECT `+personOf("u")+`
		  FROM app.users u
		 WHERE u.id <> $1::uuid AND `+limits.TeacherRole("u.role_id")+` AND `+retention.NotAnonymous+`
		   AND ($2::text = '' OR `+strings.Join(matches, " OR ")+`)
		 ORDER BY CASE WHEN $2::text = '' OR `+strings.Join(starts, " OR ")+` THEN 0 ELSE 1 END,
		          `+folded(nameOf("u"))+`, u.id
		 LIMIT $3`, []any{actorID, db.EscapeLike(strings.TrimSpace(query)), limit}, func(rows pgx.Rows) (domain.Person, error) {
		return scanPerson(rows)
	})
	if err != nil {
		return nil, fmt.Errorf("sharing: directory: %w", err)
	}
	return people, nil
}

// Person returns the user id, or ErrNotFound.
func (p *Postgres) Person(ctx context.Context, id string) (domain.Person, error) {
	person, err := scanPerson(p.QueryRow(ctx, `SELECT `+personOf("u")+` FROM app.users u WHERE u.id = $1::uuid`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Person{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Person{}, fmt.Errorf("sharing: person: %w", err)
	}
	return person, nil
}

// Holds reports whether userID holds a grant to the resource while it is live.
func (p *Postgres) Holds(ctx context.Context, kind domain.Kind, resourceID, userID string) (bool, error) {
	r := tableOf(kind)
	held, err := db.Exists(ctx, p, `SELECT 1 FROM app.content_shares g
		  JOIN `+r.table+` r ON r.id = g.`+r.column+` AND r.deleted_at IS NULL
		 WHERE g.`+r.column+` = $1::uuid AND g.grantee_id = $2::uuid`, resourceID, opt.String(userID))
	if err != nil {
		return false, fmt.Errorf("sharing: holds: %w", err)
	}
	return held, nil
}

const grantColumns = `g.id::text, g.kind, coalesce(g.test_id, g.question_set_id)::text, g.access, g.created_at, ` + `%s, b.id::text, ` + `%s
	  FROM app.content_shares g
	  JOIN app.users u ON u.id = g.grantee_id
	  JOIN app.users b ON b.id = g.granted_by`

var grantSelect = fmt.Sprintf(grantColumns, personOf("u"), nameOf("b"))

func scanGrant(rows pgx.Rows) (domain.Grant, error) {
	var g domain.Grant
	var kind, level string
	err := rows.Scan(&g.ID, &kind, &g.ResourceID, &level, &g.GrantedAt,
		&g.Grantee.ID, &g.Grantee.FullName, &g.Grantee.DisplayName, &g.Grantee.Email, &g.Grantee.AvatarKey, &g.Grantee.Disabled,
		&g.GrantedBy.ID, &g.GrantedBy.Name)
	g.Kind, g.Access = domain.Kind(kind), domain.Access(level)
	return g, err
}

// Grants returns the grants to the resource, oldest first.
func (p *Postgres) Grants(ctx context.Context, kind domain.Kind, resourceID string) ([]domain.Grant, error) {
	grants, err := db.QueryMany(ctx, p, `SELECT `+grantSelect+`
		 WHERE g.`+tableOf(kind).column+` = $1::uuid
		 ORDER BY g.created_at, g.id`, []any{resourceID}, scanGrant)
	if err != nil {
		return nil, fmt.Errorf("sharing: grants: %w", err)
	}
	return grants, nil
}

func grantsByID(ctx context.Context, q db.Querier, ids []string) ([]domain.Grant, error) {
	grants, err := db.QueryMany(ctx, q, `SELECT `+grantSelect+` WHERE g.id = ANY($1::uuid[])`, []any{ids}, scanGrant)
	if err != nil {
		return nil, fmt.Errorf("sharing: grants by id: %w", err)
	}
	return grants, nil
}

// Share returns the grant id as its row stores it, or ErrNotFound.
func (p *Postgres) Share(ctx context.Context, id string) (domain.Share, error) {
	var s domain.Share
	var kind, level string
	err := p.QueryRow(ctx, `SELECT g.id::text, g.kind, coalesce(g.test_id, g.question_set_id)::text, g.grantee_id::text, g.access
		  FROM app.content_shares g WHERE g.id = $1::uuid`, id).Scan(&s.ID, &kind, &s.ResourceID, &s.GranteeID, &level)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Share{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Share{}, fmt.Errorf("sharing: share: %w", err)
	}
	s.Kind, s.Access = domain.Kind(kind), domain.Access(level)
	return s, nil
}

const sharedRows = `
	  FROM app.content_shares g
	  LEFT JOIN app.tests t ON t.id = g.test_id AND t.deleted_at IS NULL
	  LEFT JOIN app.question_sets s ON s.id = g.question_set_id AND s.deleted_at IS NULL
	  JOIN app.users o ON o.id = coalesce(t.owner_id, s.owner_id)
	 WHERE g.grantee_id = $1::uuid AND (t.id IS NOT NULL OR s.id IS NOT NULL)`

func sharedMatch(param int) string {
	return fmt.Sprintf(`($%d::text = '' OR %s OR %s OR %s)`, param,
		contains(`coalesce(t.title, s.name)`, param), contains(`o.full_name`, param), contains(`coalesce(o.display_name, '')`, param))
}

// SharedWithMe returns one page of the live grants to f.UserID, newest grant
// first: a grant to a soft-deleted test or set is left out. The query matches
// the resource's name and its owner's names accent-insensitively; the counts
// are taken under it, before the kind.
func (p *Postgres) SharedWithMe(ctx context.Context, f domain.SharedFilter) ([]domain.SharedItem, domain.Counts, paging.Page, error) {
	number, size, offset := paging.Clamp(f.Page, f.Limit, DefaultLimit, MaxLimit)
	query := db.EscapeLike(strings.TrimSpace(f.Query))

	var counts domain.Counts
	if err := p.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE g.kind = 'test'), count(*) FILTER (WHERE g.kind = 'question_set')`+
		sharedRows+` AND `+sharedMatch(2), f.UserID, query).Scan(&counts.All, &counts.Test, &counts.QuestionSet); err != nil {
		return nil, domain.Counts{}, paging.Page{}, fmt.Errorf("sharing: shared with me counts: %w", err)
	}
	page := paging.Page{Number: number, Size: size, Total: counts.All}
	switch f.Kind {
	case domain.KindTest:
		page.Total = counts.Test
	case domain.KindQuestionSet:
		page.Total = counts.QuestionSet
	}
	items := []domain.SharedItem{}
	if offset >= page.Total {
		return items, counts, page, nil
	}
	items, err := db.QueryMany(ctx, p, `
		SELECT g.id::text, g.kind, coalesce(g.test_id, g.question_set_id)::text, coalesce(t.title, s.name),
		       o.id::text, `+nameOf("o")+`, o.avatar_key, o.disabled_at IS NOT NULL, g.access, g.created_at,
		       CASE WHEN s.id IS NOT NULL THEN (SELECT count(*)::int FROM app.question_set_items i
		         JOIN app.questions q ON q.id = i.question_id AND q.deleted_at IS NULL WHERE i.set_id = s.id) END`+
		sharedRows+` AND `+sharedMatch(2)+` AND ($3::text = '' OR g.kind = $3::text)
		 ORDER BY g.created_at DESC, g.id DESC
		 LIMIT $4 OFFSET $5`, []any{f.UserID, query, string(f.Kind), size, offset}, func(rows pgx.Rows) (domain.SharedItem, error) {
		var item domain.SharedItem
		var kind, level string
		err := rows.Scan(&item.ShareID, &kind, &item.ResourceID, &item.Name, &item.Owner.ID, &item.Owner.Name,
			&item.Owner.AvatarKey, &item.Owner.Disabled, &level, &item.SharedAt, &item.QuestionCount)
		item.Kind, item.Access = domain.Kind(kind), domain.Access(level)
		return item, err
	})
	if err != nil {
		return nil, domain.Counts{}, paging.Page{}, fmt.Errorf("sharing: shared with me: %w", err)
	}
	return items, counts, page, nil
}

// CountSharedWithMe is the number of live grants to userID: those whose test
// or set is not soft-deleted.
func (p *Postgres) CountSharedWithMe(ctx context.Context, userID string) (int, error) {
	count, err := db.Count(ctx, p, `SELECT count(*)`+sharedRows, userID)
	if err != nil {
		return 0, fmt.Errorf("sharing: count shared with me: %w", err)
	}
	return count, nil
}
