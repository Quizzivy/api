//go:build integration

package maintenance_test

import (
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"quizzivy/internal/core/adapters"
	attemptsdomain "quizzivy/internal/modules/attempts/domain"
	attemptsrepo "quizzivy/internal/modules/attempts/repositories"
	"quizzivy/internal/platform/db"
)

// begin starts an attempt on assignment at at, through the attempts store and
// its maintenance guard, for a new student it targets; with an override
// closing at closes when closes is not zero.
func (w world) begin(t *testing.T, assignment string, at, closes time.Time) (attemptsdomain.AttemptRecord, error) {
	t.Helper()
	student := uuid.NewString()
	if _, err := w.tx.Exec(w.ctx, `
		WITH s AS (INSERT INTO app.users(id,email,full_name,role_id)
		           VALUES($1::uuid,$1::text || '@example.com','Student',(SELECT id FROM app.roles WHERE builtin_key = 'student')) RETURNING id)
		INSERT INTO app.assignment_students (assignment_id, user_id) SELECT $2::uuid, id FROM s`, student, assignment); err != nil {
		t.Fatal(err)
	}
	if !closes.IsZero() {
		if _, err := w.tx.Exec(w.ctx, `
			INSERT INTO app.assignment_student_overrides (assignment_id, student_id, closes_at, reason)
			VALUES ($1::uuid, $2::uuid, $3, 'đi xa')`, assignment, student, closes); err != nil {
			t.Fatal(err)
		}
	}
	hash := sha256.Sum256([]byte(student))
	created, _, err := attemptsrepo.NewPostgres(db.NewContext(w.tx), adapters.AttemptStartGuard{}).Create(w.ctx, attemptsdomain.CreateInput{
		AssignmentID: assignment, StudentID: student, AttemptNo: 1, SessionID: uuid.NewString(), Seed: 1,
		BeaconHash: hash[:], StartedAt: at, ExpectedDeadlineAt: at.Add(time.Microsecond),
	})
	return created, err
}

// F1, "Maintenance treats a flexible attempt by its close": a flexible
// attempt that starts after a window was scheduled ends where it would have if
// it had been running when the window was scheduled. A close inside the
// window, which the schedule did not move because it was written after it,
// moves by the window's length; an assignment close inside the window lifts a
// student's override close after it to the assignment's moved close; a close
// before or after the window stays.
func TestAFlexibleStartHonoursAWindowAlreadyScheduled(t *testing.T) {
	w := newWorld(t)
	start := far(w, 21)
	end := start.Add(time.Hour)
	opens := start.Add(-30 * time.Minute)

	// The twin was running when the window was scheduled.
	twin := w.flexible(t, w.assignment(t, start.Add(20*time.Minute)))
	running := w.attempt(t, twin, w.closes(t, twin))
	w.schedule(t, start, end)

	inside := w.flexible(t, w.assignment(t, start.Add(20*time.Minute)))
	lifted := w.flexible(t, w.assignment(t, start.Add(10*time.Minute)))
	before := w.flexible(t, w.assignment(t, start.Add(-10*time.Minute)))
	after := w.flexible(t, w.assignment(t, end.Add(48*time.Hour)))

	cases := []struct {
		name       string
		assignment string
		override   time.Time
		want       time.Time
	}{
		{"a close inside the window", inside, time.Time{}, start.Add(80 * time.Minute)},
		{"an override after the window, its assignment closing inside it", lifted, end.Add(5 * time.Minute), start.Add(70 * time.Minute)},
		{"an override inside the window", before, start.Add(30 * time.Minute), start.Add(90 * time.Minute)},
		{"a close before the window", before, time.Time{}, start.Add(-10 * time.Minute)},
		{"a close after the window", after, time.Time{}, end.Add(48 * time.Hour)},
	}
	for _, c := range cases {
		created, err := w.begin(t, c.assignment, opens, c.override)
		if err != nil {
			t.Errorf("%s: the start before the window was refused: %v", c.name, err)
			continue
		}
		if !created.DeadlineAt.Equal(c.want) {
			t.Errorf("%s: the flexible attempt started after the window was scheduled has deadline %v, want %v", c.name, created.DeadlineAt, c.want)
		}
		if got := w.deadline(t, created.ID); !got.Equal(c.want) {
			t.Errorf("%s: the stored deadline is %v, want %v", c.name, got, c.want)
		}
	}
	if got := w.deadline(t, running); !got.Equal(start.Add(80 * time.Minute)) {
		t.Errorf("the twin running when the window was scheduled has deadline %v, want %v, the same as the one started after", got, start.Add(80*time.Minute))
	}
	if got := w.closes(t, inside); !got.Equal(start.Add(20 * time.Minute)) {
		t.Errorf("the start moved the assignment's close to %v; only the attempt's deadline moves", got)
	}

	var refused *attemptsdomain.MaintenanceScheduledError
	if _, err := w.begin(t, inside, start.Add(5*time.Minute), time.Time{}); !errors.As(err, &refused) {
		t.Errorf("a flexible start while the window is under way: %v, want MaintenanceScheduledError", err)
	}
}

// Windows ahead move a flexible start one after another, the earliest first:
// a close the first window moves into the second moves again.
func TestAFlexibleStartGoesThroughEveryWindowAhead(t *testing.T) {
	w := newWorld(t)
	start := far(w, 22)
	w.schedule(t, start, start.Add(time.Hour))
	w.schedule(t, start.Add(90*time.Minute), start.Add(150*time.Minute))
	closes := w.flexible(t, w.assignment(t, start.Add(40*time.Minute)))

	created, err := w.begin(t, closes, start.Add(-30*time.Minute), time.Time{})
	if err != nil {
		t.Fatalf("the start before both windows was refused: %v", err)
	}
	if want := start.Add(160 * time.Minute); !created.DeadlineAt.Equal(want) {
		t.Errorf("the flexible attempt has deadline %v, want %v: its close moved by the first window into the second, and by the second", created.DeadlineAt, want)
	}
}

// A strict start is as it was: refused when its time limit or its close meets
// a window ahead, and given its start plus its time limit, unmoved, when that
// ends before the window.
func TestAStrictStartIsNotMovedByAWindowAhead(t *testing.T) {
	w := newWorld(t)
	start := far(w, 23)
	w.schedule(t, start, start.Add(time.Hour))
	strict := w.assignment(t, start.Add(20*time.Minute))

	early := start.Add(-2 * time.Hour)
	created, err := w.begin(t, strict, early, time.Time{})
	if err != nil {
		t.Fatalf("a strict start whose 45 minutes end before the window was refused: %v", err)
	}
	if want := early.Add(45 * time.Minute); !created.DeadlineAt.Equal(want) {
		t.Errorf("the strict attempt has deadline %v, want its start plus 45 minutes, %v", created.DeadlineAt, want)
	}
	var refused *attemptsdomain.MaintenanceScheduledError
	if _, err := w.begin(t, strict, start.Add(-30*time.Minute), time.Time{}); !errors.As(err, &refused) {
		t.Errorf("a strict start whose close falls inside the window: %v, want MaintenanceScheduledError", err)
	}
}
