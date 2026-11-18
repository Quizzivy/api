//go:build integration

package maintenance_test

import (
	"context"
	"crypto/sha256"
	"math/rand/v2"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"quizzivy/internal/core/adapters"
	"quizzivy/internal/core/maintenance"
	attemptsdomain "quizzivy/internal/modules/attempts/domain"
	attemptsrepo "quizzivy/internal/modules/attempts/repositories"
	"quizzivy/internal/platform/db"
)

// holdingGuard is the real start guard, which takes the windows lock shared,
// paused on its first call once it holds the lock, until release is closed.
type holdingGuard struct {
	adapters.AttemptStartGuard
	reached, release chan struct{}
	once, freed      sync.Once
}

// free lets the paused start go on; it may be called more than once.
func (g *holdingGuard) free() { g.freed.Do(func() { close(g.release) }) }

func newHoldingGuard() *holdingGuard {
	return &holdingGuard{reached: make(chan struct{}), release: make(chan struct{})}
}

func (g *holdingGuard) GuardAttemptStart(ctx context.Context, tx pgx.Tx, now, deadline time.Time) (*attemptsdomain.MaintenanceWindow, error) {
	window, err := g.AttemptStartGuard.GuardAttemptStart(ctx, tx, now, deadline)
	g.once.Do(func() {
		close(g.reached)
		<-g.release
	})
	return window, err
}

// waitOnLock returns once another session waits on a lock in a statement
// whose text contains marker.
func waitOnLock(t *testing.T, pool *pgxpool.Pool, marker string) {
	t.Helper()
	until := time.Now().Add(15 * time.Second)
	for time.Now().Before(until) {
		var waiting int
		if err := pool.QueryRow(context.Background(), `
			SELECT count(*) FROM pg_stat_activity
			 WHERE wait_event_type = 'Lock' AND strpos(query, $1) > 0 AND pid <> pg_backend_pid()`, marker).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting > 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("no statement containing %q ever waited on a lock", marker)
}

const (
	scheduleLock = "pg_advisory_xact_lock(73819"
	sharedLock   = "pg_advisory_xact_lock_shared(73819"
)

// flexibleRace is a committed flexible assignment that closes 10 minutes into
// a window from windowStart to an hour later, which is not scheduled yet.
type flexibleRace struct {
	*committed
	windowStart time.Time
}

func commitFlexibleRace(t *testing.T) flexibleRace {
	t.Helper()
	base := time.Now().Add(40*365*24*time.Hour + time.Duration(rand.IntN(80000))*time.Hour).Truncate(time.Second)
	c := commitAssignment(t, base)
	r := flexibleRace{committed: c, windowStart: base.Add(time.Hour)}
	if _, err := c.pool.Exec(context.Background(),
		`UPDATE app.assignments SET mode = 'flexible', closes_at = $2 WHERE id = $1::uuid`,
		c.assignment, r.windowStart.Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	return r
}

// liftedStudent is a student of the assignment whose own close is 5 minutes
// after the window: the window lifts their deadline to the assignment's moved
// close, 70 minutes after the window starts, which only the window's second
// term gives (FlexibleWindowDeadline). An attempt that misses the window ends
// at the student's own close.
func (r flexibleRace) liftedStudent(t *testing.T) string {
	t.Helper()
	student := r.student(t)
	if _, err := r.pool.Exec(context.Background(), `
		INSERT INTO app.assignment_student_overrides (assignment_id, student_id, closes_at, reason, created_by)
		VALUES ($1::uuid, $2::uuid, $3, 'đi xa', $4::uuid)`,
		r.assignment, student, r.windowStart.Add(65*time.Minute), r.teacher); err != nil {
		t.Fatal(err)
	}
	return student
}

func (r flexibleRace) startInput(student string) attemptsdomain.CreateInput {
	hash := sha256.Sum256([]byte(uuid.NewString()))
	startedAt := r.windowStart.Add(-30 * time.Minute)
	return attemptsdomain.CreateInput{
		AssignmentID: r.assignment, StudentID: student, AttemptNo: 1, SessionID: uuid.NewString(), Seed: 1,
		BeaconHash: hash[:], StartedAt: startedAt, ExpectedDeadlineAt: startedAt.Add(time.Microsecond),
	}
}

func (r flexibleRace) storedDeadline(t *testing.T, attempt string) time.Time {
	t.Helper()
	var at time.Time
	if err := r.pool.QueryRow(context.Background(), `SELECT deadline_at FROM app.attempts WHERE id = $1::uuid`, attempt).Scan(&at); err != nil {
		t.Fatal(err)
	}
	return at
}

// scheduling opens a transaction that schedules the window and holds the
// windows lock exclusively until the test commits it.
func (r flexibleRace) scheduling(t *testing.T) pgx.Tx {
	t.Helper()
	ctx := context.Background()
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(ctx) })
	if _, err := maintenance.ScheduleWindow(ctx, tx, r.windowStart, r.windowStart.Add(time.Hour), true); err != nil {
		t.Fatalf("ScheduleWindow: %v", err)
	}
	return tx
}

// F1: a flexible start holds the windows lock from its maintenance check to
// its commit, so a window scheduled meanwhile waits for it and then moves the
// attempt it inserted, as it moves every attempt in progress.
func TestAWindowScheduledWhileAFlexibleStartHoldsTheLockWaitsAndMovesIt(t *testing.T) {
	r := commitFlexibleRace(t)
	ctx := context.Background()
	guard := newHoldingGuard()
	// A failure below must not leave the start holding a connection, or the
	// fixture's cleanup waits on it for ever.
	t.Cleanup(guard.free)
	starts := attemptsrepo.NewPostgres(db.NewContext(r.pool), guard)
	in := r.startInput(r.liftedStudent(t))

	var created attemptsdomain.AttemptRecord
	var startErr error
	started := make(chan struct{})
	go func() {
		defer close(started)
		created, _, startErr = starts.Create(ctx, in)
	}()
	select {
	case <-guard.reached:
	case <-started:
		t.Fatalf("the start ended before it reached the maintenance check: %v", startErr)
	case <-time.After(30 * time.Second):
		t.Fatal("the start never reached the maintenance check")
	}

	scheduled := make(chan error, 1)
	go func() {
		_, err := maintenance.ScheduleWindow(ctx, r.pool, r.windowStart, r.windowStart.Add(time.Hour), true)
		scheduled <- err
	}()
	waitOnLock(t, r.pool, scheduleLock)
	select {
	case err := <-scheduled:
		t.Fatalf("the window was scheduled (%v) while a flexible start held the windows lock", err)
	default:
	}

	guard.free()
	<-started
	if startErr != nil {
		t.Fatalf("start: %v", startErr)
	}
	select {
	case err := <-scheduled:
		if err != nil {
			t.Fatalf("ScheduleWindow: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the window was never scheduled after the start committed")
	}
	if got, want := r.storedDeadline(t, created.ID), r.windowStart.Add(70*time.Minute); !got.Equal(want) {
		t.Errorf("the flexible attempt that started before the window was scheduled has deadline %v, want %v", got, want)
	}
}

// F1: a flexible start that comes while a window is being scheduled waits
// at its maintenance check for the schedule to commit, and then reads its
// rules with the closes the schedule moved, so it ends where an attempt
// running when the window was scheduled ends. In this order ThroughWindows
// has nothing left to add: the closes it would move are already moved.
func TestAFlexibleStartWhileAWindowIsBeingScheduledWaitsAndIsMovedByIt(t *testing.T) {
	r := commitFlexibleRace(t)
	ctx := context.Background()
	student := r.liftedStudent(t)
	tx := r.scheduling(t)
	starts := attemptsrepo.NewPostgres(db.NewContext(r.pool), adapters.AttemptStartGuard{})

	var created attemptsdomain.AttemptRecord
	var startErr error
	started := make(chan struct{})
	go func() {
		defer close(started)
		created, _, startErr = starts.Create(ctx, r.startInput(student))
	}()
	waitOnLock(t, r.pool, sharedLock)
	select {
	case <-started:
		t.Fatalf("the start ended (%v) while a window was being scheduled", startErr)
	default:
	}

	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(30 * time.Second):
		t.Fatal("the start never finished after the window was scheduled")
	}
	if startErr != nil {
		t.Fatalf("start: %v", startErr)
	}
	want := r.windowStart.Add(70 * time.Minute)
	if !created.DeadlineAt.Equal(want) {
		t.Errorf("the flexible start that waited for the window has deadline %v, want %v", created.DeadlineAt, want)
	}
	if got := r.storedDeadline(t, created.ID); !got.Equal(want) {
		t.Errorf("the stored deadline is %v, want %v", got, want)
	}
}

// F1: a teacher's extension of a flexible attempt that comes while a window
// is being scheduled waits for the schedule to commit, and is then moved
// through the window it extended into (schedule.MoveAttemptThroughWindows).
func TestAnExtensionWhileAWindowIsBeingScheduledWaitsAndIsMovedByIt(t *testing.T) {
	r := commitFlexibleRace(t)
	ctx := context.Background()
	// The attempt ends 10 minutes before the window, so scheduling it neither
	// moves nor locks the attempt row.
	var attempt string
	if err := r.pool.QueryRow(ctx, `
		INSERT INTO app.attempts (assignment_id, test_version_id, student_id, attempt_no, session_id, shuffle_seed, beacon_token_hash, started_at, deadline_at)
		VALUES ($1::uuid, $2::uuid, $3::uuid, 1, gen_random_uuid(), 1, sha256('b'::bytea), $4::timestamptz - interval '30 minutes', $4::timestamptz - interval '10 minutes')
		RETURNING id::text`, r.assignment, r.version, r.student(t), r.windowStart).Scan(&attempt); err != nil {
		t.Fatal(err)
	}
	tx := r.scheduling(t)
	store := attemptsrepo.NewPostgres(db.NewContext(r.pool), adapters.AttemptStartGuard{})

	extended := make(chan error, 1)
	go func() {
		_, err := store.Extend(ctx, attemptsdomain.Request{ActorID: r.teacher, All: true}, attempt, 30, "mất điện", time.Now())
		extended <- err
	}()
	waitOnLock(t, r.pool, sharedLock)
	select {
	case err := <-extended:
		t.Fatalf("the extension finished (%v) while a window was being scheduled", err)
	default:
	}

	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-extended:
		if err != nil {
			t.Fatalf("extend: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the extension never finished after the window was scheduled")
	}
	if got, want := r.storedDeadline(t, attempt), r.windowStart.Add(80*time.Minute); !got.Equal(want) {
		t.Errorf("the flexible attempt extended 20 minutes into the window has deadline %v, want %v", got, want)
	}
}
