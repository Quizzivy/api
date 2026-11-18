//go:build integration

package maintenance_test

import (
	"testing"
	"time"

	"quizzivy/internal/core/adapters"
	attemptsdomain "quizzivy/internal/modules/attempts/domain"
	attemptsrepo "quizzivy/internal/modules/attempts/repositories"
	"quizzivy/internal/platform/db"
)

// attemptMove is one attempt.extended audit row of an attempt.
type attemptMove struct {
	reason, window, cause, actor string
	minutes                      *int
	old, new                     time.Time
}

func (w world) movesOf(t *testing.T, attempt string) []attemptMove {
	t.Helper()
	rows, err := w.tx.Query(w.ctx, `
		SELECT diff->>'reason', coalesce(diff->>'windowId', ''), coalesce(diff->>'cause', ''),
		       coalesce(actor_user_id::text, ''), (diff->>'minutes')::int,
		       (diff->'deadline_at'->>'old')::timestamptz, (diff->'deadline_at'->>'new')::timestamptz
		  FROM app.audit_log
		 WHERE action = 'attempt.extended' AND entity_id = $1::uuid
		 ORDER BY (diff->'deadline_at'->>'new')::timestamptz`, attempt)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []attemptMove
	for rows.Next() {
		var m attemptMove
		if err := rows.Scan(&m.reason, &m.window, &m.cause, &m.actor, &m.minutes, &m.old, &m.new); err != nil {
			t.Fatal(err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func (w world) teacherOf(t *testing.T, assignment string) string {
	t.Helper()
	var id string
	if err := w.tx.QueryRow(w.ctx, `SELECT created_by::text FROM app.assignments WHERE id = $1`, assignment).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// F1, "Maintenance treats a flexible attempt by its close": a teacher's
// extension of one flexible attempt into a window already scheduled is kept
// past the window, as scheduling the window after the extension would have
// kept it (the window's first term), and that move is audited apart. A strict
// attempt is extended by the minutes alone, as before.
func TestATeachersExtensionOfAFlexibleAttemptIntoAScheduledWindowIsKeptPastIt(t *testing.T) {
	w := newWorld(t)
	start := far(w, 24)
	end := start.Add(time.Hour)
	flexible := w.flexible(t, w.assignment(t, start.Add(-10*time.Minute)))
	strict := w.assignment(t, start.Add(48*time.Hour))
	flexibleAttempt := w.attempt(t, flexible, start.Add(-10*time.Minute))
	strictAttempt := w.attempt(t, strict, start.Add(-10*time.Minute))
	report := w.schedule(t, start, end)
	if got := w.deadline(t, flexibleAttempt); !got.Equal(start.Add(-10 * time.Minute)) {
		t.Fatalf("the window moved a flexible attempt ending before it, to %v", got)
	}

	store := attemptsrepo.NewPostgres(db.NewContext(w.tx), adapters.AttemptStartGuard{})
	teacher := w.teacherOf(t, flexible)
	req := attemptsdomain.Request{ActorID: teacher, All: true}
	extended, err := store.Extend(w.ctx, req, flexibleAttempt, 30, "mất điện", w.now)
	if err != nil {
		t.Fatal(err)
	}
	want := start.Add(80 * time.Minute)
	if !extended.DeadlineAt.Equal(want) {
		t.Errorf("Extend answers deadline %v for the flexible attempt it moved into the window, want its end plus the 20 minutes the extension left inside it, %v", extended.DeadlineAt, want)
	}
	if got := w.deadline(t, flexibleAttempt); !got.Equal(want) {
		t.Errorf("the flexible attempt extended into the window has deadline %v, want %v", got, want)
	}
	moves := w.movesOf(t, flexibleAttempt)
	if len(moves) != 2 {
		t.Fatalf("the flexible attempt has %d attempt.extended rows %+v, want the extension's and one for the window", len(moves), moves)
	}
	if m := moves[0]; m.reason != "mất điện" || m.minutes == nil || *m.minutes != 30 || m.window != "" ||
		!m.old.Equal(start.Add(-10*time.Minute)) || !m.new.Equal(start.Add(20*time.Minute)) {
		t.Errorf("the extension is audited as %+v", m)
	}
	if m := moves[1]; m.reason != "maintenance" || m.window != report.WindowID || m.cause != "attempt_extended" ||
		m.actor != teacher || !m.old.Equal(start.Add(20*time.Minute)) || !m.new.Equal(want) {
		t.Errorf("the move through the window is audited as %+v, want maintenance, window %s, cause attempt_extended, actor %s, %v to %v",
			m, report.WindowID, teacher, start.Add(20*time.Minute), want)
	}

	if _, err := store.Extend(w.ctx, req, strictAttempt, 30, "mất điện", w.now); err != nil {
		t.Fatal(err)
	}
	if got, want := w.deadline(t, strictAttempt), start.Add(20*time.Minute); !got.Equal(want) {
		t.Errorf("the strict attempt has deadline %v, want the extension's 30 minutes alone, %v, as before", got, want)
	}
	if moves := w.movesOf(t, strictAttempt); len(moves) != 1 || moves[0].reason != "mất điện" {
		t.Errorf("the strict attempt's moves are audited as %+v, want the extension's alone", moves)
	}
}
