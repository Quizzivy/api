//go:build integration

package repositories_test

import (
	"context"
	"testing"
	"time"

	"quizzivy/internal/modules/assignments/domain"
	"quizzivy/internal/shared/schedule"
)

// windowAhead schedules a maintenance window in the test's transaction, as
// window-schedule would have before the writes that follow, and returns its id.
func (w *reachWorld) windowAhead(t *testing.T, startsAt, endsAt time.Time) string {
	t.Helper()
	return w.id(t, `INSERT INTO app.maintenance_windows (starts_at, ends_at) VALUES ($1, $2) RETURNING id::text`, startsAt, endsAt)
}

func (w *reachWorld) flexibleClosingSoon(t *testing.T, closesIn time.Duration) string {
	t.Helper()
	in := input(w.versionA, []string{w.classA}, nil)
	now := time.Now()
	in.OpensAt, in.ClosesAt, in.DurationMin, in.Mode = now.Add(-time.Hour), now.Add(closesIn).Truncate(time.Second), 45, schedule.Flexible
	return w.create(t, as(w.a, false), in)
}

// attemptMove is one attempt.extended audit row of an attempt.
type attemptMove struct {
	reason, window, cause, actor string
	old, new                     time.Time
}

// movesOf returns the attempt.extended rows of attempt, in the order the
// deadline moved.
func (w *reachWorld) movesOf(t *testing.T, attempt string) []attemptMove {
	t.Helper()
	rows, err := w.tx.Query(context.Background(), `
		SELECT diff->>'reason', coalesce(diff->>'windowId', ''), coalesce(diff->>'cause', ''),
		       coalesce(actor_user_id::text, ''),
		       (diff->'deadline_at'->>'old')::timestamptz, (diff->'deadline_at'->>'new')::timestamptz
		  FROM app.audit_log
		 WHERE action = 'attempt.extended' AND entity_id = $1::uuid
		 ORDER BY (diff->'deadline_at'->>'new')::timestamptz, (diff->'deadline_at'->>'old')::timestamptz`, attempt)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []attemptMove
	for rows.Next() {
		var m attemptMove
		if err := rows.Scan(&m.reason, &m.window, &m.cause, &m.actor, &m.old, &m.new); err != nil {
			t.Fatal(err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// wantMoves checks that attempt's audited moves are exactly want, in order.
func (w *reachWorld) wantMoves(t *testing.T, what, attempt string, want ...attemptMove) {
	t.Helper()
	got := w.movesOf(t, attempt)
	if len(got) != len(want) {
		t.Errorf("%s: %d attempt.extended rows %+v, want %d %+v", what, len(got), got, len(want), want)
		return
	}
	for i := range want {
		g, x := got[i], want[i]
		if g.reason != x.reason || g.window != x.window || g.cause != x.cause || g.actor != x.actor || !g.old.Equal(x.old) || !g.new.Equal(x.new) {
			t.Errorf("%s: move %d is audited as %+v, want %+v", what, i, g, x)
		}
	}
}

func (w *reachWorld) extendBy(t *testing.T, assignment string, minutes int) {
	t.Helper()
	req := as(w.a, false)
	req.ID = assignment
	if _, err := w.store.Extend(context.Background(), req, minutes, false, time.Now()); err != nil {
		t.Fatal(err)
	}
}

// F1, "Maintenance treats a flexible attempt by its close": an extension that
// moves a flexible assignment's close into a window already scheduled moves
// its attempts in progress past the window, as scheduling the window after
// the extension would have, and audits that move apart; a strict attempt is
// moved as before, by its rule alone.
func TestAnExtensionIntoAScheduledWindowMovesAFlexibleAttemptPastIt(t *testing.T) {
	w := newReachWorld(t)
	base := time.Now().Truncate(time.Second)
	window := w.windowAhead(t, base.Add(40*time.Minute), base.Add(100*time.Minute))

	flexible := w.flexibleClosingSoon(t, 20*time.Minute)
	strict := w.closingSoon(t, w.a, w.classA, 20*time.Minute, 45)
	flexibleAttempt := w.sitting(t, flexible, w.classmate(t, w.classA, w.a), 5*time.Minute, w.closesAt(t, flexible), "in_progress")
	// Started a minute ago, the strict attempt's deadline after the extension,
	// its start plus 45 minutes, is some four minutes into the window, so
	// the move through the window would select it if it did not keep to
	// flexible attempts.
	strictAttempt := w.sitting(t, strict, w.classmate(t, w.classA, w.a), time.Minute, w.closesAt(t, strict), "in_progress")

	closes := w.closesAt(t, flexible)
	w.extendBy(t, flexible, 30)
	w.extendBy(t, strict, 30)

	want := closes.Add(30*time.Minute + time.Hour)
	if got := w.deadlineOf(t, flexibleAttempt); !got.Equal(want) {
		t.Errorf("the flexible attempt has deadline %v after its close moved into the window, want the close plus the window's hour, %v", got, want)
	}
	w.wantMoves(t, "the flexible attempt", flexibleAttempt,
		attemptMove{reason: "assignment_extended", actor: w.a, old: closes, new: closes.Add(30 * time.Minute)},
		attemptMove{reason: "maintenance", window: window, cause: "assignment_extended", actor: w.a, old: closes.Add(30 * time.Minute), new: want})
	if got, want := w.deadlineOf(t, strictAttempt), w.startedOf(t, strictAttempt).Add(45*time.Minute); !got.Equal(want) {
		t.Errorf("the strict attempt has deadline %v, want its start plus 45 minutes, %v, as before", got, want)
	}
	w.wantMoves(t, "the strict attempt", strictAttempt,
		attemptMove{reason: "assignment_extended", actor: w.a, old: w.closesAt(t, strict).Add(-30 * time.Minute), new: w.startedOf(t, strictAttempt).Add(45 * time.Minute)})

	// A later write that changes nothing leaves the attempt where the window
	// put it, and audits no move.
	w.extendBy(t, flexible, 0)
	if got := w.deadlineOf(t, flexibleAttempt); !got.Equal(want) {
		t.Errorf("an extension by nothing moved the flexible attempt to %v, want it kept at %v", got, want)
	}
	w.wantMoves(t, "the flexible attempt after an extension by nothing", flexibleAttempt,
		attemptMove{reason: "assignment_extended", actor: w.a, old: closes, new: closes.Add(30 * time.Minute)},
		attemptMove{reason: "maintenance", window: window, cause: "assignment_extended", actor: w.a, old: closes.Add(30 * time.Minute), new: want})
}

// An override whose close falls inside a window already scheduled moves the
// student's flexible attempt past the window with it; the other students'
// attempts stay.
func TestAnOverrideIntoAScheduledWindowMovesAFlexibleAttemptPastIt(t *testing.T) {
	w := newReachWorld(t)
	ctx := context.Background()
	base := time.Now().Truncate(time.Second)
	window := w.windowAhead(t, base.Add(40*time.Minute), base.Add(100*time.Minute))

	flexible := w.flexibleClosingSoon(t, 20*time.Minute)
	closes := w.closesAt(t, flexible)
	named, other := w.classmate(t, w.classA, w.a), w.classmate(t, w.classA, w.a)
	namedAttempt := w.sitting(t, flexible, named, 5*time.Minute, closes, "in_progress")
	otherAttempt := w.sitting(t, flexible, other, 5*time.Minute, closes, "in_progress")

	req := as(w.a, false)
	req.ID = flexible
	own := base.Add(70 * time.Minute)
	if _, err := w.store.SetOverrides(ctx, req, domain.OverrideInput{
		StudentIDs: []string{named}, ClosesAt: &own, Reason: "đi xa", Now: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	if got, want := w.deadlineOf(t, namedAttempt), own.Add(time.Hour); !got.Equal(want) {
		t.Errorf("the named student's flexible attempt has deadline %v, want the override's close plus the window's hour, %v", got, want)
	}
	if got := w.deadlineOf(t, otherAttempt); !got.Equal(closes) {
		t.Errorf("another student's attempt moved to %v, want it kept at %v", got, closes)
	}
	w.wantMoves(t, "the named student's attempt", namedAttempt,
		attemptMove{reason: "student_override", actor: w.a, old: closes, new: own},
		attemptMove{reason: "maintenance", window: window, cause: "student_override", actor: w.a, old: own, new: own.Add(time.Hour)})
	w.wantMoves(t, "another student's attempt", otherAttempt)
}

// F1: an update that moves a flexible assignment's close into a window
// already scheduled moves its attempts in progress past the window, audited
// with the update as the cause.
func TestAnUpdateIntoAScheduledWindowMovesAFlexibleAttemptPastIt(t *testing.T) {
	w := newReachWorld(t)
	ctx := context.Background()
	base := time.Now().Truncate(time.Second)
	window := w.windowAhead(t, base.Add(40*time.Minute), base.Add(100*time.Minute))
	flexible := w.flexibleClosingSoon(t, 20*time.Minute)
	closes := w.closesAt(t, flexible)
	attempt := w.sitting(t, flexible, w.classmate(t, w.classA, w.a), 5*time.Minute, closes, "in_progress")

	req := as(w.a, false)
	req.ID = flexible
	inside := base.Add(60 * time.Minute)
	if _, err := w.store.Update(ctx, req, w.updateWindow(t, inside)); err != nil {
		t.Fatalf("update: %v", err)
	}
	if got, want := w.deadlineOf(t, attempt), inside.Add(time.Hour); !got.Equal(want) {
		t.Errorf("the flexible attempt has deadline %v after the update moved its close into the window, want %v", got, want)
	}
	w.wantMoves(t, "the flexible attempt", attempt,
		attemptMove{reason: "assignment_updated", actor: w.a, old: closes, new: inside},
		attemptMove{reason: "maintenance", window: window, cause: "assignment_updated", actor: w.a, old: inside, new: inside.Add(time.Hour)})
}

// F1: a writer moves a flexible attempt through every window ahead, the
// earliest first: a deadline the first window moves into the second moves
// again, and each move is audited with its own window.
func TestAWriterMovesAFlexibleAttemptThroughEveryWindowAhead(t *testing.T) {
	w := newReachWorld(t)
	base := time.Now().Truncate(time.Second)
	first := w.windowAhead(t, base.Add(40*time.Minute), base.Add(100*time.Minute))
	second := w.windowAhead(t, base.Add(105*time.Minute), base.Add(165*time.Minute))
	flexible := w.flexibleClosingSoon(t, 20*time.Minute)
	closes := w.closesAt(t, flexible)
	attempt := w.sitting(t, flexible, w.classmate(t, w.classA, w.a), 5*time.Minute, closes, "in_progress")

	w.extendBy(t, flexible, 30)
	extended, throughFirst, throughSecond := closes.Add(30*time.Minute), closes.Add(90*time.Minute), closes.Add(150*time.Minute)
	if got := w.deadlineOf(t, attempt); !got.Equal(throughSecond) {
		t.Errorf("the flexible attempt has deadline %v, want %v: moved by the first window into the second, and by the second", got, throughSecond)
	}
	w.wantMoves(t, "the flexible attempt", attempt,
		attemptMove{reason: "assignment_extended", actor: w.a, old: closes, new: extended},
		attemptMove{reason: "maintenance", window: first, cause: "assignment_extended", actor: w.a, old: extended, new: throughFirst},
		attemptMove{reason: "maintenance", window: second, cause: "assignment_extended", actor: w.a, old: throughFirst, new: throughSecond})
}
