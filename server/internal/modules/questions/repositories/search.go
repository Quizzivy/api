package repositories

import (
	"context"
	"fmt"
	"quizzivy/internal/modules/questions/domain"
	"quizzivy/internal/platform/db"
	"quizzivy/internal/shared/opt"
	"quizzivy/internal/shared/paging"
	"strings"
)

// Page-size bounds for the bank listing.
const (
	DefaultLimit = 20
	MaxLimit     = 100
)

// TrigramExpression must match questions_prompt_trgm_idx verbatim; Postgres
// uses an expression index only on an identical expression. Shared with the
// EXPLAIN test in search_test.go.
const TrigramExpression = `app.immutable_unaccent(lower(q.prompt))`

const searchCondition = `(
		to_tsvector('simple', q.prompt) @@ plainto_tsquery('simple', $%[1]d)
		OR ` + TrigramExpression + ` LIKE '%%' || app.immutable_unaccent(lower($%[1]d)) || '%%' ESCAPE '\'
	)`

func bankRows(in domain.ListInput) ([]any, []string) {
	where := []string{`q.deleted_at IS NULL`, `q.context_group_id IS NULL`}
	if in.SetID != "" {
		return []any{in.SetID}, append(where, `q.id IN (SELECT i.question_id FROM app.question_set_items i WHERE i.set_id = $1::uuid)`)
	}
	if in.Scope.All {
		return nil, where
	}
	return []any{opt.String(in.Scope.UserID)}, append(where, `q.owner_id = $1::uuid`)
}

func appendFilters(in domain.ListInput, opts filterOpts) ([]any, []string) {
	args, where := bankRows(in)

	if opts.types && len(in.Types) > 0 {
		types := make([]string, len(in.Types))
		for i, t := range in.Types {
			types[i] = string(t)
		}
		args = append(args, types)
		where = append(where, fmt.Sprintf(`q.type = ANY($%d::app.question_type[])`, len(args)))
	}
	if opts.tags && len(in.Tags) > 0 {
		args = append(args, in.Tags)
		operator := "&&"
		if in.TagMatch == "all" {
			operator = "@>"
		}
		where = append(where, fmt.Sprintf(`q.tags %s $%d::text[]`, operator, len(args)))
	}
	args, where = appendMetadataFilters(in, opts, args, where)
	if in.HasAudio != nil {
		args = append(args, *in.HasAudio)
		where = append(where, fmt.Sprintf(
			`(q.media_asset_kind = 'audio') = $%d::boolean`, len(args)))
	}
	if q := strings.TrimSpace(in.Query); q != "" {
		args = append(args, db.EscapeLike(q))
		where = append(where, fmt.Sprintf(searchCondition, len(args)))
	}
	return args, where
}

type filterOpts struct {
	types  bool
	tags   bool
	levels bool
	skills bool
}

func allFilters() filterOpts {
	return filterOpts{types: true, tags: true, levels: true, skills: true}
}

// List returns one page of live bank questions, newest first, or a set's in
// set order, each with its owner, with the paging beside it (O-20: OFFSET, so
// the client can draw numbered pages).
func (s *Postgres) List(ctx context.Context, in domain.ListInput) ([]domain.Question, paging.Page, error) {
	number, limit, offset := paging.Clamp(in.Page, in.Limit, DefaultLimit, MaxLimit)

	args, where := appendFilters(in, allFilters())
	from := `
		  FROM app.questions q
		 WHERE ` + strings.Join(where, "\n		   AND ")

	page := paging.Page{Number: number, Size: limit}
	if err := s.QueryRow(ctx, `SELECT count(*)`+from, args...).Scan(&page.Total); err != nil {
		return nil, paging.Page{}, fmt.Errorf("questions: count: %w", err)
	}

	columns, order := questionColumns+`, q.owner_id::text, (SELECT `+personName+` FROM app.users u WHERE u.id = q.owner_id)`, `q.id DESC`
	if in.SetID != "" {
		order = `(SELECT i.ordinal FROM app.question_set_items i WHERE i.set_id = $1::uuid AND i.question_id = q.id), q.id`
	}
	args = append(args, limit, offset)
	rows, err := s.Query(ctx, `SELECT`+columns+from+fmt.Sprintf(`
		 ORDER BY %s
		 LIMIT $%d OFFSET $%d`, order, len(args)-1, len(args)), args...)
	if err != nil {
		return nil, paging.Page{}, fmt.Errorf("questions: list: %w", err)
	}
	defer rows.Close()

	questions := make([]domain.Question, 0, limit)
	for rows.Next() {
		var owner domain.Owner
		question, err := scanQuestion(rows, &owner.ID, &owner.Name)
		if err != nil {
			return nil, paging.Page{}, err
		}
		question.Owner = &owner
		questions = append(questions, question)
	}
	if err := rows.Err(); err != nil {
		return nil, paging.Page{}, fmt.Errorf("questions: list: %w", err)
	}

	if err := s.attachChildren(ctx, questions); err != nil {
		return nil, paging.Page{}, err
	}
	return questions, page, nil
}

func (s *Postgres) attachChildren(ctx context.Context, questions []domain.Question) error {
	return s.loadChildren(ctx, s.Conn(), questions)
}

func appendMetadataFilters(in domain.ListInput, opts filterOpts, args []any, where []string) ([]any, []string) {
	if opts.levels && len(in.Levels) > 0 {
		values := make([]string, len(in.Levels))
		for i, value := range in.Levels {
			values[i] = string(value)
		}
		args = append(args, values)
		where = append(where, fmt.Sprintf(`q.level = ANY($%d::text[])`, len(args)))
	}
	if opts.skills && len(in.Skills) > 0 {
		values := make([]string, len(in.Skills))
		for i, value := range in.Skills {
			values[i] = string(value)
		}
		args = append(args, values)
		where = append(where, fmt.Sprintf(`q.skill = ANY($%d::text[])`, len(args)))
	}
	return args, where
}
