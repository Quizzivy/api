//go:build integration

package application_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	organizationmodel "quizzivy/internal/modules/organization/application/model"
	"quizzivy/internal/modules/timetable/application"
	"quizzivy/internal/modules/timetable/domain"
	"quizzivy/internal/modules/timetable/repositories"
	"quizzivy/internal/platform/db"
	"quizzivy/internal/shared/access"
	"quizzivy/internal/shared/actor"
)

type counter struct {
	mu sync.Mutex
	n  int
}

func (c *counter) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.n++
	return ctx
}

func (c *counter) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func (c *counter) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n
}

type terms map[string]time.Time

func (t terms) CurrentTerm(_ context.Context, on time.Time) (*organizationmodel.Term, error) {
	for starts, ends := range t {
		first, _ := time.Parse("2006-01-02", starts)
		if !on.Before(first) && !on.After(ends) {
			return &organizationmodel.Term{ID: "term", Name: "Học kỳ 1", StartsOn: first, EndsOn: ends}, nil
		}
	}
	return nil, nil
}

type zones map[string]string

func (z zones) ZoneOf(_ context.Context, userID string) (string, error) { return z[userID], nil }

type world struct {
	t       *testing.T
	tx      pgx.Tx
	queries *counter
	app     *application.Application
	zones   zones
	terms   terms
	now     time.Time
	marker  string
	a, b    string
}

var hcm = domain.LocationOf("Asia/Ho_Chi_Minh")

func newWorld(t *testing.T) *world {
	t.Helper()
	cfg, err := pgxpool.ParseConfig(db.TestDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	queries := &counter{}
	cfg.ConnConfig.Tracer = queries
	cfg.MinConns, cfg.MaxConns = 1, 1
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	tx, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	w := &world{
		t: t, tx: tx, queries: queries, zones: zones{}, terms: terms{},
		now:    time.Date(2026, 11, 23, 10, 0, 0, 0, hcm),
		marker: "tt" + strings.ReplaceAll(uuid.NewString()[:8], "-", ""),
	}
	w.app = application.New(repositories.NewPostgres(db.NewContext(tx))).WithTerms(w.terms).WithZones(w.zones)
	w.app.SetClock(func() time.Time { return w.now })
	w.a, w.b = w.user("Cô An"), w.user("Thầy Bình")
	return w
}

func (w *world) id(sql string, args ...any) string {
	w.t.Helper()
	var id string
	if err := w.tx.QueryRow(context.Background(), sql, args...).Scan(&id); err != nil {
		w.t.Fatalf("%s: %v", sql, err)
	}
	return id
}

func (w *world) exec(sql string, args ...any) {
	w.t.Helper()
	if _, err := w.tx.Exec(context.Background(), sql, args...); err != nil {
		w.t.Fatalf("%s: %v", sql, err)
	}
}

func (w *world) user(name string) string {
	w.t.Helper()
	return w.id(`INSERT INTO app.users (email, full_name, role_id)
		VALUES ($1, $2, (SELECT id FROM app.roles WHERE builtin_key = 'teacher')) RETURNING id::text`,
		w.marker+"-"+uuid.NewString()[:8]+"@example.test", name+" "+w.marker)
}

func (w *world) student() string {
	w.t.Helper()
	return w.id(`INSERT INTO app.users (email, full_name, role_id)
		VALUES ($1, $2, (SELECT id FROM app.roles WHERE builtin_key = 'student')) RETURNING id::text`,
		w.marker+"-"+uuid.NewString()[:8]+"@example.test", "Học viên "+w.marker)
}

func (w *world) class(teacher string, room *string) string {
	w.t.Helper()
	return w.id(`INSERT INTO app.classes (name, teacher_id, room) VALUES ($1, $2::uuid, $3) RETURNING id::text`,
		"Lớp "+w.marker+" "+uuid.NewString()[:4], teacher, room)
}

func (w *world) mark(sessionRow, student, teacher string) {
	w.t.Helper()
	w.exec(`INSERT INTO app.attendance_marks (session_id, student_id, status, marked_by, marked_at)
		VALUES ($1::uuid, $2::uuid, 'present', $3::uuid, now())`, sessionRow, student, teacher)
}

func by(id string) actor.Actor {
	return actor.Actor{ID: id, IP: "203.0.113.9", UserAgent: "timetable-test", Scope: access.Scope{UserID: id}}
}

func day(s string) time.Time {
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return d
}

func at(s string) time.Time {
	v, err := time.ParseInLocation("2006-01-02 15:04", s, hcm)
	if err != nil {
		panic(err)
	}
	return v
}

func ptr[T any](v T) *T { return &v }

type entry struct {
	Action, Entity, EntityID string
	Diff                     map[string]any
}

func (w *world) audited(actorID string) []entry {
	w.t.Helper()
	rows, err := w.tx.Query(context.Background(), `SELECT action, entity, coalesce(entity_id::text, ''), coalesce(diff, '{}'::jsonb)
		  FROM app.audit_log WHERE actor_user_id = $1::uuid ORDER BY id`, actorID)
	if err != nil {
		w.t.Fatal(err)
	}
	defer rows.Close()
	var out []entry
	for rows.Next() {
		var e entry
		var raw []byte
		if err := rows.Scan(&e.Action, &e.Entity, &e.EntityID, &raw); err != nil {
			w.t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &e.Diff); err != nil {
			w.t.Fatal(err)
		}
		out = append(out, e)
	}
	return out
}

func (w *world) rowsOf(series string) map[string]string {
	w.t.Helper()
	rows, err := w.tx.Query(context.Background(), `SELECT to_char(series_date, 'YYYY-MM-DD'), coalesce(note, '') || '|' || (cancelled_at IS NOT NULL)::text
		  FROM app.class_sessions WHERE series_id = $1::uuid`, series)
	if err != nil {
		w.t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var date, facts string
		if err := rows.Scan(&date, &facts); err != nil {
			w.t.Fatal(err)
		}
		out[date] = facts
	}
	return out
}
