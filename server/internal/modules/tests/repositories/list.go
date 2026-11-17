package repositories

import (
	"context"
	"fmt"
	"quizzivy/internal/modules/tests/domain"
	"quizzivy/internal/platform/db"
	"quizzivy/internal/shared/opt"
	"quizzivy/internal/shared/paging"
	"quizzivy/internal/shared/visibility"
	"strings"
)

const (
	DefaultLimit = 20
	MaxLimit     = 100
)

const titleSearch = `app.immutable_unaccent(lower(t.title))` +
	` LIKE '%%' || app.immutable_unaccent(lower($%[1]d)) || '%%' ESCAPE '\'`

const tagCondition = `EXISTS (SELECT 1 FROM (` + draftQuestionRows + `) q WHERE q.tags && $%d::text[])`

// scopedTests is the condition and the arguments of a list of tests: the live
// tests the scope's user owns, every live test under scope.all, and, with
// IncludeShared, those shared with the user too. The scope's scope.all and
// user id are placeholders $1 and $2, so testAccess("t", 1, 2) is each row's
// access for the reader.
func scopedTests(in domain.ListInput) ([]string, []any) {
	reach := `($1::boolean OR t.owner_id = $2::uuid)`
	if in.IncludeShared {
		reach = `($1::boolean OR t.owner_id = $2::uuid OR t.id IN ` + visibility.SharedTestIDs(2) + `)`
	}
	return []string{liveTests, reach}, []any{in.Scope.All, opt.String(in.Scope.UserID)}
}

// List returns one page of live tests, newest first, each with the reader's
// access, and the paging that goes with it (O-20: OFFSET, so the client can
// draw numbered pages).
func (s *Postgres) List(ctx context.Context, in domain.ListInput) ([]domain.Test, paging.Page, error) {
	number, limit, offset := paging.Clamp(in.Page, in.Limit, DefaultLimit, MaxLimit)

	where, args := scopedTests(in)
	if in.Status != nil {
		args = append(args, string(*in.Status))
		where = append(where, fmt.Sprintf(`t.status = $%d::app.test_status`, len(args)))
	}
	if len(in.Tags) > 0 {
		args = append(args, in.Tags)
		where = append(where, fmt.Sprintf(tagCondition, len(args)))
	}
	if q := strings.TrimSpace(in.Query); q != "" {
		args = append(args, db.EscapeLike(q))
		where = append(where, fmt.Sprintf(titleSearch, len(args)))
	}
	filter := `
		 WHERE ` + strings.Join(where, "\n		   AND ")

	page := paging.Page{Number: number, Size: limit}
	if err := s.QueryRow(ctx, `SELECT count(*) FROM app.tests t`+filter, args...).Scan(&page.Total); err != nil {
		return nil, paging.Page{}, fmt.Errorf("tests: count: %w", err)
	}

	args = append(args, limit, offset)
	rows, err := s.Query(ctx, `SELECT`+testColumns+`, `+testAccess("t", 1, 2)+testFrom+filter+fmt.Sprintf(`
		 ORDER BY t.id DESC
		 LIMIT $%d OFFSET $%d`, len(args)-1, len(args)), args...)
	if err != nil {
		return nil, paging.Page{}, fmt.Errorf("tests: list: %w", err)
	}
	defer rows.Close()

	list := make([]domain.Test, 0, limit)
	for rows.Next() {
		var level string
		t, err := scanTest(rows, &level)
		if err != nil {
			return nil, paging.Page{}, err
		}
		readerView(&t, domain.Access(level))
		list = append(list, t)
	}
	if err := rows.Err(); err != nil {
		return nil, paging.Page{}, fmt.Errorf("tests: list: %w", err)
	}

	if err := s.attachSections(ctx, list); err != nil {
		return nil, paging.Page{}, err
	}
	return list, page, nil
}

func (s *Postgres) attachSections(ctx context.Context, list []domain.Test) error {
	if len(list) == 0 {
		return nil
	}
	ids := make([]string, len(list))
	for i, t := range list {
		ids[i] = t.ID
	}

	byTest, err := s.sectionsFor(ctx, s.Conn(), ids)
	if err != nil {
		return err
	}
	for i := range list {
		list[i].Sections = []domain.Section{}
		if got, ok := byTest[list[i].ID]; ok {
			list[i].Sections = got
		}
	}
	return nil
}

// Tags returns every tag reachable through the current status and search, so
// A-03's filter cannot offer a chip that returns nothing.
func (s *Postgres) Tags(ctx context.Context, in domain.ListInput) ([]string, error) {
	where, args := scopedTests(in)
	if in.Status != nil {
		args = append(args, string(*in.Status))
		where = append(where, fmt.Sprintf(`t.status = $%d::app.test_status`, len(args)))
	}
	if q := strings.TrimSpace(in.Query); q != "" {
		args = append(args, db.EscapeLike(q))
		where = append(where, fmt.Sprintf(titleSearch, len(args)))
	}

	rows, err := s.Query(ctx, `
		SELECT DISTINCT unnest(q.tags)
		  FROM app.tests t
		  JOIN LATERAL (`+draftQuestionRows+`) q ON true
		 WHERE `+strings.Join(where, " AND ")+`
		 ORDER BY 1`, args...)
	if err != nil {
		return nil, fmt.Errorf("tests: tags: %w", err)
	}
	defer rows.Close()

	out := []string{}
	for rows.Next() {
		var tag string
		if err := rows.Scan(&tag); err != nil {
			return nil, fmt.Errorf("tests: scan tag: %w", err)
		}
		out = append(out, tag)
	}
	return out, rows.Err()
}
