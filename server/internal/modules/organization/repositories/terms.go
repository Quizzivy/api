package repositories

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"quizzivy/internal/modules/organization/domain"
	"quizzivy/internal/platform/db"
	"quizzivy/internal/shared/audit"
	"quizzivy/internal/shared/opt"
)

// Terms stores the centre's terms in app.terms (T-R8.3).
type Terms struct{ db.Repository }

// NewTerms builds the terms store on the database context.
func NewTerms(dbx db.Context) *Terms { return &Terms{Repository: db.NewRepository(dbx)} }

var _ domain.Terms = (*Terms)(nil)

const (
	termColumns = `id::text, name, starts_on, ends_on`
	// overlapConstraint is 00175's exclusion constraint over the inclusive ranges.
	overlapConstraint = "terms_no_overlap"
	// overlapTries bounds the writes of a range whose 23P01 named a term that
	// was gone by the time it was read: each try is judged by the database again.
	overlapTries = 3
	dayLayout    = "2006-01-02"
)

func scanTerm(row pgx.Row) (domain.Term, error) {
	var t domain.Term
	if err := row.Scan(&t.ID, &t.Name, &t.StartsOn, &t.EndsOn); err != nil {
		return domain.Term{}, err
	}
	t.StartsOn, t.EndsOn = domain.Day(t.StartsOn), domain.Day(t.EndsOn)
	return t, nil
}

func scanTerms(rows pgx.Rows) (domain.Term, error) { return scanTerm(rows) }

// List returns every term, latest first day first.
func (r *Terms) List(ctx context.Context) ([]domain.Term, error) {
	out, err := db.QueryMany(ctx, r, `SELECT `+termColumns+` FROM app.terms ORDER BY starts_on DESC, id DESC`, nil, scanTerms)
	if err != nil {
		return nil, fmt.Errorf("organization: list terms: %w", err)
	}
	return out, nil
}

// On returns the term that holds day. Terms never overlap, so there is one at most.
func (r *Terms) On(ctx context.Context, day time.Time) (*domain.Term, error) {
	t, err := scanTerm(r.QueryRow(ctx, `SELECT `+termColumns+` FROM app.terms
		WHERE starts_on <= $1::date AND ends_on >= $1::date`, day.Format(dayLayout)))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("organization: the term on %s: %w", day.Format(dayLayout), err)
	}
	return &t, nil
}

// EndingFrom returns the terms whose last day is day or later, earliest first.
func (r *Terms) EndingFrom(ctx context.Context, day time.Time) ([]domain.Term, error) {
	out, err := db.QueryMany(ctx, r, `SELECT `+termColumns+` FROM app.terms WHERE ends_on >= $1::date ORDER BY starts_on, id`,
		[]any{day.Format(dayLayout)}, scanTerms)
	if err != nil {
		return nil, fmt.Errorf("organization: the terms ending from %s: %w", day.Format(dayLayout), err)
	}
	return out, nil
}

// Create stores draft with its term.created row.
func (r *Terms) Create(ctx context.Context, draft domain.TermDraft, by domain.TermWriter) (domain.Term, error) {
	var out domain.Term
	err := r.InTx(ctx, "organization: create term", func(tx pgx.Tx) error {
		var err error
		out, err = writeRange(ctx, tx, draft, "", func(q pgx.Tx) (domain.Term, error) {
			return scanTerm(q.QueryRow(ctx, `
				INSERT INTO app.terms (name, starts_on, ends_on, created_by)
				VALUES ($1, $2::date, $3::date, $4::uuid)
				RETURNING `+termColumns,
				draft.Name, draft.StartsOn.Format(dayLayout), draft.EndsOn.Format(dayLayout), opt.String(by.ActorID)))
		})
		if err != nil {
			return err
		}
		return recordTerm(ctx, tx, "term.created", out.ID, by, termFacts(out))
	})
	if err != nil {
		return domain.Term{}, err
	}
	return out, nil
}

// Update locks term id, applies change to it and stores the result with its
// term.updated row, which names the fields that changed with their values
// before and after. A draft equal to the stored term writes nothing.
func (r *Terms) Update(ctx context.Context, id string, change func(domain.Term) (domain.TermDraft, error), by domain.TermWriter) (domain.Term, error) {
	var out domain.Term
	err := r.InTx(ctx, "organization: update term", func(tx pgx.Tx) error {
		stored, err := scanTerm(tx.QueryRow(ctx, `SELECT `+termColumns+` FROM app.terms WHERE id = $1::uuid FOR UPDATE`, id))
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrTermNotFound
		}
		if err != nil {
			return fmt.Errorf("organization: read term %s: %w", id, err)
		}
		draft, err := change(stored)
		if err != nil {
			return err
		}
		next := domain.Term{ID: stored.ID, Name: draft.Name, StartsOn: draft.StartsOn, EndsOn: draft.EndsOn}
		fields := stored.ChangedFields(next)
		if len(fields) == 0 {
			out = stored
			return nil
		}
		out, err = writeRange(ctx, tx, draft, id, func(q pgx.Tx) (domain.Term, error) {
			return scanTerm(q.QueryRow(ctx, `
				UPDATE app.terms SET name = $2, starts_on = $3::date, ends_on = $4::date
				 WHERE id = $1::uuid
				RETURNING `+termColumns,
				id, draft.Name, draft.StartsOn.Format(dayLayout), draft.EndsOn.Format(dayLayout)))
		})
		if err != nil {
			return err
		}
		before, after := termFacts(stored), termFacts(out)
		from, to := map[string]string{}, map[string]string{}
		for _, f := range fields {
			from[f], to[f] = before[f], after[f]
		}
		return recordTerm(ctx, tx, "term.updated", id, by, map[string]any{"fields": fields, "from": from, "to": to})
	})
	if err != nil {
		return domain.Term{}, err
	}
	return out, nil
}

// Delete removes term id with its term.deleted row, which keeps the name and
// the range the term had.
func (r *Terms) Delete(ctx context.Context, id string, by domain.TermWriter) error {
	return r.InTx(ctx, "organization: delete term", func(tx pgx.Tx) error {
		gone, err := scanTerm(tx.QueryRow(ctx, `DELETE FROM app.terms WHERE id = $1::uuid RETURNING `+termColumns, id))
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrTermNotFound
		}
		if err != nil {
			return fmt.Errorf("organization: delete term %s: %w", id, err)
		}
		return recordTerm(ctx, tx, "term.deleted", id, by, termFacts(gone))
	})
}

// writeRange runs write, which stores draft's range, under a savepoint. When
// the database refuses the range under terms_no_overlap, the savepoint is
// rolled back and the term it shares a day with, other than self, is read and
// returned as *domain.TermOverlap. The refusal is the database's: nothing is
// looked up before the write. When that term is gone by the time it is read,
// the write is tried again, at most overlapTries times in all.
func writeRange(ctx context.Context, tx pgx.Tx, draft domain.TermDraft, self string, write func(pgx.Tx) (domain.Term, error)) (domain.Term, error) {
	for range overlapTries {
		point, err := tx.Begin(ctx)
		if err != nil {
			return domain.Term{}, fmt.Errorf("organization: savepoint: %w", err)
		}
		t, err := write(point)
		if err == nil {
			return t, point.Commit(ctx)
		}
		_ = point.Rollback(ctx)
		if !db.IsExclusionViolation(err, overlapConstraint) {
			return domain.Term{}, fmt.Errorf("organization: write term: %w", err)
		}
		other, err := scanTerm(tx.QueryRow(ctx, `SELECT `+termColumns+` FROM app.terms
			WHERE daterange(starts_on, ends_on, '[]') && daterange($1::date, $2::date, '[]')
			  AND id IS DISTINCT FROM $3::uuid
			ORDER BY starts_on, id LIMIT 1`,
			draft.StartsOn.Format(dayLayout), draft.EndsOn.Format(dayLayout), opt.String(self)))
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return domain.Term{}, fmt.Errorf("organization: read the overlapped term: %w", err)
		}
		return domain.Term{}, &domain.TermOverlap{Other: other}
	}
	return domain.Term{}, fmt.Errorf("organization: write term: the range met a term %d times that was gone when read", overlapTries)
}

func termFacts(t domain.Term) map[string]string {
	return map[string]string{"name": t.Name, "startsOn": t.StartsOn.Format(dayLayout), "endsOn": t.EndsOn.Format(dayLayout)}
}

func recordTerm(ctx context.Context, tx pgx.Tx, action, id string, by domain.TermWriter, facts any) error {
	diff, err := json.Marshal(facts)
	if err != nil {
		return fmt.Errorf("organization: encode %s diff: %w", action, err)
	}
	return audit.Write(ctx, tx, audit.Entry{
		ActorUserID: opt.String(by.ActorID), Action: action, Entity: "term", EntityID: &id,
		OccurredAt: by.Now, IP: opt.String(by.IP), UserAgent: opt.String(by.UserAgent), Diff: diff,
	})
}
