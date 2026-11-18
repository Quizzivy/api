//go:build integration

package maintenance_test

import (
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"quizzivy/internal/core/adapters"
	"quizzivy/internal/core/maintenance"
	attemptsdomain "quizzivy/internal/modules/attempts/domain"
	attemptsrepo "quizzivy/internal/modules/attempts/repositories"
	"quizzivy/internal/platform/db"
)

func (w world) flexible(t *testing.T, assignment string) string {
	t.Helper()
	if _, err := w.tx.Exec(w.ctx, `UPDATE app.assignments SET mode = 'flexible' WHERE id = $1`, assignment); err != nil {
		t.Fatal(err)
	}
	return assignment
}

func (w world) overrideFor(t *testing.T, attempt string, closesAt time.Time) {
	t.Helper()
	if _, err := w.tx.Exec(w.ctx, `
		INSERT INTO app.assignment_student_overrides (assignment_id, student_id, closes_at, reason, created_by)
		SELECT at.assignment_id, at.student_id, $2, 'đi xa', a.created_by
		  FROM app.attempts at JOIN app.assignments a ON a.id = at.assignment_id WHERE at.id = $1`, attempt, closesAt); err != nil {
		t.Fatal(err)
	}
}

// F1: scheduling a window recomputes a flexible attempt's deadline from its
// student's close, as the same statement moves that close, instead of adding
// the window's length; a strict attempt is extended as before.
func TestAWindowMovesAFlexibleAttemptOnlyWithItsStudentsClose(t *testing.T) {
	w := newWorld(t)
	start := far(w, 11)
	end := start.Add(time.Hour)
	inside := w.flexible(t, w.assignment(t, start.Add(20*time.Minute)))
	beyond := w.flexible(t, w.assignment(t, end.Add(48*time.Hour)))
	strict := w.assignment(t, end.Add(48*time.Hour))
	before := w.flexible(t, w.assignment(t, start.Add(-10*time.Minute)))
	closingInside := w.attempt(t, inside, w.closes(t, inside))
	closingBeyond := w.attempt(t, beyond, w.closes(t, beyond))
	ownClose := w.attempt(t, before, start.Add(30*time.Minute))
	w.overrideFor(t, ownClose, start.Add(30*time.Minute))
	timed := w.attempt(t, strict, start.Add(10*time.Minute))

	dry, err := maintenance.ScheduleWindow(w.ctx, w.tx, start, end, false)
	if err != nil {
		t.Fatal(err)
	}
	report := w.schedule(t, start, end)

	if got, want := w.deadline(t, closingInside), start.Add(80*time.Minute); !got.Equal(want) || !w.closes(t, inside).Equal(want) {
		t.Errorf("a flexible attempt whose close falls inside the window has deadline %v, want the moved close %v", got, want)
	}
	if got, want := w.deadline(t, closingBeyond), end.Add(48*time.Hour); !got.Equal(want) {
		t.Errorf("a flexible attempt whose close is after the window moved to %v, want it kept at %v", got, want)
	}
	if got, want := w.deadline(t, ownClose), start.Add(90*time.Minute); !got.Equal(want) {
		t.Errorf("a flexible attempt whose override closes inside the window has deadline %v, want the moved override %v", got, want)
	}
	if got, want := w.deadline(t, timed), start.Add(70*time.Minute); !got.Equal(want) {
		t.Errorf("a strict attempt has deadline %v, want %v, its own plus the window's length", got, want)
	}
	var audited int
	if err := w.tx.QueryRow(w.ctx, `SELECT count(*) FROM app.audit_log WHERE action = 'attempt.extended' AND entity_id = $1`, closingBeyond).Scan(&audited); err != nil {
		t.Fatal(err)
	}
	if audited != 0 || dry.Attempts != report.Attempts {
		t.Errorf("the unmoved flexible attempt has %d extension rows; the dry run counted %d attempts, the run %d", audited, dry.Attempts, report.Attempts)
	}
}

// A flexible start is refused only while a window is under way; a window
// ahead of it, before its close, does not refuse it, where it refuses a
// strict start whose time limit meets it.
func TestAFlexibleStartIsRefusedOnlyWhileAWindowIsUnderWay(t *testing.T) {
	w := newWorld(t)
	start := far(w, 12)
	w.schedule(t, start.Add(10*time.Minute), start.Add(70*time.Minute))
	flexible := w.flexible(t, w.assignment(t, start.Add(72*time.Hour)))
	strict := w.assignment(t, start.Add(72*time.Hour))
	starts := attemptsrepo.NewPostgres(db.NewContext(w.tx), adapters.AttemptStartGuard{})
	begin := func(assignment string, at time.Time) (attemptsdomain.AttemptRecord, error) {
		t.Helper()
		student := uuid.NewString()
		if _, err := w.tx.Exec(w.ctx, `
			WITH s AS (INSERT INTO app.users(id,email,full_name,role_id)
			           VALUES($1::uuid,$1::text || '@example.com','Student',(SELECT id FROM app.roles WHERE builtin_key = 'student')) RETURNING id)
			INSERT INTO app.assignment_students (assignment_id, user_id) SELECT $2::uuid, id FROM s`, student, assignment); err != nil {
			t.Fatal(err)
		}
		hash := sha256.Sum256([]byte(student))
		created, _, err := starts.Create(w.ctx, attemptsdomain.CreateInput{
			AssignmentID: assignment, StudentID: student, AttemptNo: 1, SessionID: uuid.NewString(), Seed: 1,
			BeaconHash: hash[:], StartedAt: at, ExpectedDeadlineAt: at.Add(time.Microsecond),
		})
		return created, err
	}

	created, err := begin(flexible, start)
	if err != nil {
		t.Fatalf("a flexible start before the window was refused: %v", err)
	}
	if want := start.Add(72 * time.Hour); !created.DeadlineAt.Equal(want) {
		t.Errorf("the flexible attempt's deadline is %v, want its close %v", created.DeadlineAt, want)
	}
	var refused *attemptsdomain.MaintenanceScheduledError
	if _, err := begin(strict, start); !errors.As(err, &refused) {
		t.Errorf("a strict start whose 45 minutes meet the window: %v, want MaintenanceScheduledError", err)
	}
	if _, err := begin(flexible, start.Add(20*time.Minute)); !errors.As(err, &refused) {
		t.Errorf("a flexible start while the window is under way: %v, want MaintenanceScheduledError", err)
	}
}
