//go:build integration

package tests

import (
	"context"
	"encoding/json"
	"errors"
	"math/rand/v2"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"quizzivy/internal/modules/organization/application"
	"quizzivy/internal/modules/organization/application/command"
	"quizzivy/internal/modules/organization/application/query"
	"quizzivy/internal/modules/organization/domain"
	"quizzivy/internal/modules/organization/repositories"
	"quizzivy/internal/platform/db"
	"quizzivy/internal/shared/actor"
	"quizzivy/internal/shared/validation"
)

// termWorld is the terms store on the app role, inside a transaction that is
// rolled back. Terms are organisation-wide and never overlap, so each world
// writes in a year of its own, far from any other test's.
type termWorld struct {
	t     *testing.T
	tx    pgx.Tx
	app   *application.Application
	admin actor.Actor
	year  int
}

func openTermWorld(t *testing.T) *termWorld {
	t.Helper()
	dsn := os.Getenv("APP_DATABASE_URL")
	if dsn == "" {
		t.Skip("APP_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := tx.Rollback(context.Background()); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
			t.Errorf("rollback: %v", err)
		}
	})
	var admin string
	if err := tx.QueryRow(ctx, `INSERT INTO app.users (email, full_name, role_id)
		VALUES ('terms-' || uuidv7() || '@example.com', 'Quản trị', (SELECT id FROM app.roles WHERE builtin_key = 'admin'))
		RETURNING id::text`).Scan(&admin); err != nil {
		t.Fatal(err)
	}
	app, _ := build(t, &fakeRepo{settings: stored()})
	app.WithTerms(repositories.NewTerms(db.NewContext(tx)))
	return &termWorld{
		t: t, tx: tx, app: app, year: 3000 + rand.IntN(6000),
		admin: actor.Actor{ID: admin, IP: "203.0.113.9", UserAgent: "terms-test"},
	}
}

// on is the day in the world's year, or the years after it.
func (w *termWorld) on(years int, m time.Month, d int) time.Time {
	return time.Date(w.year+years, m, d, 0, 0, 0, 0, time.UTC)
}

func (w *termWorld) create(name string, from, to time.Time) (domain.StatedTerm, error) {
	w.t.Helper()
	return w.app.Commands.CreateTerm.Handle(context.Background(), command.CreateTerm{
		Actor: w.admin, Draft: domain.TermDraft{Name: name, StartsOn: from, EndsOn: to},
	})
}

func (w *termWorld) mustCreate(name string, from, to time.Time) domain.Term {
	w.t.Helper()
	got, err := w.create(name, from, to)
	if err != nil {
		w.t.Fatalf("create %s: %v", name, err)
	}
	return got.Term
}

func (w *termWorld) update(id string, patch domain.TermPatch) (domain.StatedTerm, error) {
	w.t.Helper()
	return w.app.Commands.UpdateTerm.Handle(context.Background(), command.UpdateTerm{Actor: w.admin, ID: id, Patch: patch})
}

func (w *termWorld) stored(id string) (domain.Term, bool) {
	w.t.Helper()
	listed, err := w.app.Queries.ListTerms.Handle(context.Background(), query.ListTerms{})
	if err != nil {
		w.t.Fatal(err)
	}
	for _, t := range listed {
		if t.ID == id {
			return t.Term, true
		}
	}
	return domain.Term{}, false
}

type auditRow struct {
	Action, Entity, EntityID, Actor, IP string
	Diff                                map[string]any
}

func (w *termWorld) audits(id string) []auditRow {
	w.t.Helper()
	rows, err := w.tx.Query(context.Background(), `
		SELECT action, entity, entity_id::text, actor_user_id::text, host(ip), diff
		  FROM app.audit_log WHERE entity_id = $1::uuid ORDER BY id`, id)
	if err != nil {
		w.t.Fatal(err)
	}
	defer rows.Close()
	var out []auditRow
	for rows.Next() {
		var r auditRow
		var diff []byte
		if err := rows.Scan(&r.Action, &r.Entity, &r.EntityID, &r.Actor, &r.IP, &diff); err != nil {
			w.t.Fatal(err)
		}
		if err := json.Unmarshal(diff, &r.Diff); err != nil {
			w.t.Fatal(err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		w.t.Fatal(err)
	}
	return out
}

func overlapOf(t *testing.T, err error) domain.Term {
	t.Helper()
	var overlap *domain.TermOverlap
	if !errors.As(err, &overlap) {
		t.Fatalf("got %v, want a *domain.TermOverlap", err)
	}
	return overlap.Other
}

func TestATermIsCreatedTrimmedListedNewestFirstAndAudited(t *testing.T) {
	w := openTermWorld(t)
	first := w.mustCreate("Học kỳ 1", w.on(0, 9, 1), w.on(1, 1, 15))
	created, err := w.create("  Học kỳ 2  ", w.on(1, 2, 1), w.on(1, 6, 30))
	if err != nil {
		t.Fatal(err)
	}
	if created.Name != "Học kỳ 2" || !created.StartsOn.Equal(w.on(1, 2, 1)) || !created.EndsOn.Equal(w.on(1, 6, 30)) {
		t.Errorf("created %+v, want the trimmed name and the range sent", created.Term)
	}
	if created.State != domain.TermUpcoming {
		t.Errorf("a term in %d is %s today, want upcoming", w.year+1, created.State)
	}

	listed, err := w.app.Queries.ListTerms.Handle(context.Background(), query.ListTerms{})
	if err != nil {
		t.Fatal(err)
	}
	var ours []string
	for _, term := range listed {
		if term.ID == first.ID || term.ID == created.ID {
			ours = append(ours, term.ID)
		}
	}
	if !slices.Equal(ours, []string{created.ID, first.ID}) {
		t.Errorf("listed %v, want the later term first", ours)
	}

	rows := w.audits(created.ID)
	if len(rows) != 1 {
		t.Fatalf("%d audit rows for the new term, want one", len(rows))
	}
	r := rows[0]
	if r.Action != "term.created" || r.Entity != "term" || r.Actor != w.admin.ID || r.IP != w.admin.IP {
		t.Errorf("audit row %+v, want term.created about the term by the admin", r)
	}
	want := map[string]any{"name": "Học kỳ 2", "startsOn": w.on(1, 2, 1).Format(time.DateOnly), "endsOn": w.on(1, 6, 30).Format(time.DateOnly)}
	for k, v := range want {
		if r.Diff[k] != v {
			t.Errorf("the audit diff's %s is %v, want %v", k, r.Diff[k], v)
		}
	}
}

// Both ends are inclusive, so a term that starts on another's last day
// overlaps it, and one that starts the day after does not. The refusal names
// the term it met and stores nothing, not even an audit row.
func TestATermThatSharesADayWithAnotherIsRefusedNamingIt(t *testing.T) {
	w := openTermWorld(t)
	autumn := w.mustCreate("Học kỳ 1", w.on(0, 9, 1), w.on(0, 12, 31))
	cases := map[string][2]time.Time{
		"starting on its last day":   {w.on(0, 12, 31), w.on(1, 3, 1)},
		"ending on its first day":    {w.on(0, 6, 1), w.on(0, 9, 1)},
		"inside it":                  {w.on(0, 10, 1), w.on(0, 10, 2)},
		"around it":                  {w.on(0, 1, 1), w.on(1, 12, 31)},
		"the very same range":        {w.on(0, 9, 1), w.on(0, 12, 31)},
		"one day, its last":          {w.on(0, 12, 31), w.on(0, 12, 31)},
		"one day, its first, alone ": {w.on(0, 9, 1), w.on(0, 9, 1)},
	}
	for name, r := range cases {
		_, err := w.create("Trùng "+name, r[0], r[1])
		if other := overlapOf(t, err); other.ID != autumn.ID || other.Name != "Học kỳ 1" {
			t.Errorf("%s: the refusal names %+v, want %s", name, other, autumn.ID)
		}
	}
	var refused int
	if err := w.tx.QueryRow(context.Background(), `SELECT count(*) FROM app.terms WHERE name LIKE 'Trùng %'`).Scan(&refused); err != nil || refused != 0 {
		t.Errorf("%d refused terms stored (%v), want none", refused, err)
	}
	var audited int
	if err := w.tx.QueryRow(context.Background(), `SELECT count(*) FROM app.audit_log WHERE action = 'term.created' AND diff ->> 'name' LIKE 'Trùng %'`).Scan(&audited); err != nil || audited != 0 {
		t.Errorf("%d audit rows for refused terms (%v), want none", audited, err)
	}

	if _, err := w.create("Học kỳ 2", w.on(1, 1, 1), w.on(1, 5, 31)); err != nil {
		t.Errorf("a term starting the day after another ends: %v, want it stored", err)
	}
}

func TestATermThatWouldEndBeforeItStartsIsRefusedNamingTheField(t *testing.T) {
	w := openTermWorld(t)
	_, err := w.create("Ngược", w.on(0, 9, 2), w.on(0, 9, 1))
	var invalid *validation.Error
	if !errors.As(err, &invalid) || len(invalid.Fields) != 1 || invalid.Fields[0].Field != "endsOn" {
		t.Fatalf("got %v, want a validation error naming endsOn", err)
	}
}

// An edit is judged as the term stands after it, by the same constraint: a
// term moved onto its neighbour is refused naming the neighbour and keeps its
// range, a range turned backwards is refused naming endsOn, an edit that
// changes nothing writes nothing, and one that does writes the fields it
// changed with their values before and after.
func TestEditingATermIntoAnotherIsRefusedAndChangesNothing(t *testing.T) {
	w := openTermWorld(t)
	autumn := w.mustCreate("Học kỳ 1", w.on(0, 9, 1), w.on(0, 12, 31))
	spring := w.mustCreate("Học kỳ 2", w.on(1, 1, 1), w.on(1, 5, 31))

	back := w.on(0, 12, 31)
	_, err := w.update(spring.ID, domain.TermPatch{StartsOn: &back})
	if other := overlapOf(t, err); other.ID != autumn.ID {
		t.Errorf("moving spring onto autumn's last day names %s, want autumn %s", other.ID, autumn.ID)
	}
	if got, _ := w.stored(spring.ID); !got.StartsOn.Equal(w.on(1, 1, 1)) {
		t.Errorf("after the refusal spring starts on %s, want it unchanged", got.StartsOn.Format(time.DateOnly))
	}
	over := w.on(1, 1, 1)
	if _, err := w.update(autumn.ID, domain.TermPatch{EndsOn: &over}); overlapOf(t, err).ID != spring.ID {
		t.Errorf("stretching autumn onto spring's first day does not name spring")
	}

	early := w.on(0, 12, 1)
	var invalid *validation.Error
	if _, err := w.update(spring.ID, domain.TermPatch{EndsOn: &early}); !errors.As(err, &invalid) || invalid.Fields[0].Field != "endsOn" {
		t.Errorf("ending spring before it starts: %v, want a validation error naming endsOn", err)
	}

	same := "Học kỳ 2"
	if _, err := w.update(spring.ID, domain.TermPatch{Name: &same}); err != nil {
		t.Fatal(err)
	}
	if rows := w.audits(spring.ID); len(rows) != 1 {
		t.Errorf("an edit that changes nothing left %d audit rows, want only term.created", len(rows))
	}

	renamed, later := "  Học kỳ xuân  ", w.on(1, 6, 15)
	saved, err := w.update(spring.ID, domain.TermPatch{Name: &renamed, EndsOn: &later})
	if err != nil {
		t.Fatal(err)
	}
	if saved.Name != "Học kỳ xuân" || !saved.StartsOn.Equal(w.on(1, 1, 1)) || !saved.EndsOn.Equal(later) {
		t.Errorf("saved %+v, want the trimmed name and the later end, the start kept", saved.Term)
	}
	rows := w.audits(spring.ID)
	if len(rows) != 2 || rows[1].Action != "term.updated" || rows[1].Entity != "term" {
		t.Fatalf("audit rows %+v, want term.created then term.updated", rows)
	}
	diff := rows[1].Diff
	if fields, _ := diff["fields"].([]any); len(fields) != 2 || fields[0] != "name" || fields[1] != "endsOn" {
		t.Errorf("the update names the fields %v, want [name endsOn]", diff["fields"])
	}
	from, _ := diff["from"].(map[string]any)
	to, _ := diff["to"].(map[string]any)
	if from["name"] != "Học kỳ 2" || to["name"] != "Học kỳ xuân" || to["endsOn"] != later.Format(time.DateOnly) || from["startsOn"] != nil {
		t.Errorf("the update's diff is %v, want the changed fields before and after only", diff)
	}

	ghost := "019535d9-3df7-79fb-b466-fa907fa17f9e"
	if _, err := w.update(ghost, domain.TermPatch{Name: &same}); !errors.Is(err, domain.ErrTermNotFound) {
		t.Errorf("editing an unknown term: %v, want ErrTermNotFound", err)
	}
}

func TestADeletedTermIsGoneAndAudited(t *testing.T) {
	w := openTermWorld(t)
	term := w.mustCreate("Học kỳ hè", w.on(0, 6, 1), w.on(0, 8, 31))
	del := func() error {
		_, err := w.app.Commands.DeleteTerm.Handle(context.Background(), command.DeleteTerm{Actor: w.admin, ID: term.ID})
		return err
	}
	if err := del(); err != nil {
		t.Fatal(err)
	}
	if _, ok := w.stored(term.ID); ok {
		t.Error("the deleted term is still listed")
	}
	rows := w.audits(term.ID)
	if len(rows) != 2 || rows[1].Action != "term.deleted" || rows[1].Entity != "term" || rows[1].Diff["name"] != "Học kỳ hè" {
		t.Errorf("audit rows %+v, want term.created then term.deleted keeping the name", rows)
	}
	if err := del(); !errors.Is(err, domain.ErrTermNotFound) {
		t.Errorf("deleting it again: %v, want ErrTermNotFound", err)
	}
	if _, err := w.create("Học kỳ hè mới", w.on(0, 6, 1), w.on(0, 8, 31)); err != nil {
		t.Errorf("a term over the deleted one's days: %v, want it stored", err)
	}
}

// The reads other modules take: the term that holds a day, on its first and
// last day, and the terms ending on or after a day.
func TestTheTermOnADayAndTheTermsEndingFromIt(t *testing.T) {
	w := openTermWorld(t)
	autumn := w.mustCreate("Học kỳ 1", w.on(0, 9, 1), w.on(0, 12, 31))
	spring := w.mustCreate("Học kỳ 2", w.on(1, 2, 1), w.on(1, 5, 31))
	ctx := context.Background()
	for on, want := range map[time.Time]string{
		w.on(0, 8, 31): "", w.on(0, 9, 1): autumn.ID, w.on(0, 12, 31): autumn.ID,
		w.on(1, 1, 15): "", w.on(1, 2, 1): spring.ID, w.on(1, 5, 31): spring.ID, w.on(1, 6, 1): "",
	} {
		got, err := w.app.Queries.CurrentTerm.Handle(ctx, query.CurrentTerm{On: on})
		if err != nil {
			t.Fatal(err)
		}
		if (got == nil) != (want == "") || (got != nil && got.ID != want) {
			t.Errorf("the term on %s is %+v, want %q", on.Format(time.DateOnly), got, want)
		}
	}
	ours := func(on time.Time) []string {
		t.Helper()
		ending, err := w.app.Queries.TermsEndingFrom.Handle(ctx, query.TermsEndingFrom{On: on})
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, term := range ending {
			if term.ID == autumn.ID || term.ID == spring.ID {
				out = append(out, term.ID)
			}
		}
		return out
	}
	if got := ours(w.on(0, 12, 31)); !slices.Equal(got, []string{autumn.ID, spring.ID}) {
		t.Errorf("ending from autumn's last day: %v, want autumn then spring", got)
	}
	if got := ours(w.on(1, 1, 1)); !slices.Equal(got, []string{spring.ID}) {
		t.Errorf("ending from the day after autumn: %v, want spring alone", got)
	}
}
