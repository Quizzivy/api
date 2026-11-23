//go:build integration

package application_test

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"quizzivy/internal/modules/timetable/application"
	"quizzivy/internal/modules/timetable/application/command"
	"quizzivy/internal/modules/timetable/domain"
	"quizzivy/internal/modules/timetable/repositories"
	"quizzivy/internal/platform/db"
)

// committed is a world whose rows are committed, so separate connections
// contend for them: the shared transaction of newWorld turns every save into
// one connection's statement, where nothing ever waits. Its cleanup removes
// what it made, marks and sessions before the class, and the class before
// its accounts.
type committed struct {
	t       *testing.T
	pool    *pgxpool.Pool
	app     *application.Application
	marker  string
	users   []string
	classes []string
}

func newCommitted(t *testing.T) *committed {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), db.TestDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	c := &committed{t: t, pool: pool, marker: "ta" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")}
	c.app = application.New(repositories.NewPostgres(db.NewContext(pool)))
	c.app.SetClock(func() time.Time { return at("2026-11-23 10:00") })
	t.Cleanup(func() {
		c.cleanUp()
		pool.Close()
	})
	return c
}

func (c *committed) cleanUp() {
	ctx := context.Background()
	for _, sql := range []string{
		`DELETE FROM app.attendance_marks m USING app.class_sessions s WHERE s.id = m.session_id AND s.class_id = ANY($1::uuid[])`,
		`DELETE FROM app.class_sessions WHERE class_id = ANY($1::uuid[])`,
		`DELETE FROM app.class_session_series WHERE class_id = ANY($1::uuid[])`,
		`DELETE FROM app.class_members WHERE class_id = ANY($1::uuid[])`,
		`DELETE FROM app.classes WHERE id = ANY($1::uuid[])`,
	} {
		if _, err := c.pool.Exec(ctx, sql, c.classes); err != nil {
			c.t.Errorf("cleanup: %v", err)
			return
		}
	}
	for _, id := range slices.Backward(c.users) {
		if _, err := c.pool.Exec(ctx, `DELETE FROM app.users WHERE id = $1::uuid`, id); err != nil {
			c.t.Errorf("cleanup: %v", err)
		}
	}
}

func (c *committed) id(sql string, args ...any) string {
	c.t.Helper()
	var id string
	if err := c.pool.QueryRow(context.Background(), sql, args...).Scan(&id); err != nil {
		c.t.Fatalf("%s: %v", sql, err)
	}
	return id
}

func (c *committed) user(builtin string) string {
	c.t.Helper()
	id := c.id(`INSERT INTO app.users (email, full_name, role_id)
		VALUES ($1, $2, (SELECT id FROM app.roles WHERE builtin_key = $3)) RETURNING id::text`,
		c.marker+"-"+uuid.NewString()[:8]+"@example.test", "Người "+c.marker, builtin)
	c.users = append(c.users, id)
	return id
}

func (c *committed) count(sql string, args ...any) int {
	c.t.Helper()
	var n int
	if err := c.pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		c.t.Fatalf("%s: %v", sql, err)
	}
	return n
}

// TestTwoSavesRacingForAnOccurrenceMakeOneRow holds the class row's lock
// while two saves on this morning's occurrence, which has no row yet, start
// and queue behind it, then lets them go: both succeed, the occurrence has one
// row, and both students' marks are on it, with an audit row each.
func TestTwoSavesRacingForAnOccurrenceMakeOneRow(t *testing.T) {
	c := newCommitted(t)
	teacher := c.user("teacher")
	first, second := c.user("student"), c.user("student")
	class := c.id(`INSERT INTO app.classes (name, teacher_id) VALUES ($1, $2::uuid) RETURNING id::text`, "Lớp "+c.marker, teacher)
	c.classes = append(c.classes, class)
	for _, s := range []string{first, second} {
		c.id(`INSERT INTO app.class_members (class_id, user_id, joined_via) VALUES ($1::uuid, $2::uuid, 'admin') RETURNING user_id::text`, class, s)
	}
	created, err := c.app.Commands.CreateSession.Handle(context.Background(), command.CreateSession{Actor: by(teacher), ClassID: class, Draft: weekly("2026-11-02", "08:00")})
	if err != nil {
		t.Fatal(err)
	}
	session := domain.OccurrenceID(created.Series.ID, day("2026-11-23")).String()

	ctx := context.Background()
	blocker, err := c.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = blocker.Rollback(ctx) }()
	if _, err := blocker.Exec(ctx, `SELECT 1 FROM app.classes WHERE id = $1::uuid FOR NO KEY UPDATE`, class); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, student := range []string{first, second} {
		wg.Go(func() {
			_, errs[i] = c.app.Commands.SaveAttendance.Handle(ctx, command.SaveAttendance{
				Actor: by(teacher), SessionID: session, Marks: []domain.MarkInput{set(student, domain.Present)},
			})
		})
	}
	waiting := 0
	for deadline := time.Now().Add(10 * time.Second); waiting < 2 && time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		waiting = c.count(`SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock' AND query LIKE '%FROM app.classes c%FOR NO KEY UPDATE%'`)
	}
	if waiting < 2 {
		t.Errorf("%d saves queued behind the class row's lock, want 2: the race was not staged", waiting)
	}
	if err := blocker.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("save %d: %v", i+1, err)
		}
	}
	if n := c.count(`SELECT count(*) FROM app.class_sessions WHERE series_id = $1::uuid AND series_date = '2026-11-23'`, created.Series.ID); n != 1 {
		t.Errorf("the occurrence has %d rows, want 1", n)
	}
	if n := c.count(`SELECT count(*) FROM app.attendance_marks m JOIN app.class_sessions s ON s.id = m.session_id WHERE s.series_id = $1::uuid`, created.Series.ID); n != 2 {
		t.Errorf("the occurrence holds %d marks, want 2", n)
	}
	if n := c.count(`SELECT count(*) FROM app.audit_log WHERE actor_user_id = $1::uuid AND action = 'attendance.saved'`, teacher); n != 2 {
		t.Errorf("the saves wrote %d audit rows, want 2", n)
	}
}
