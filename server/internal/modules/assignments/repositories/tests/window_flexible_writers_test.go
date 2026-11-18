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

func (w *reachWorld) movesOf(t *testing.T, attempt string) map[string]string {
	t.Helper()
	rows, err := w.tx.Query(context.Background(), `
		SELECT diff->>'reason', coalesce(diff->>'windowId', '') FROM app.audit_log
		 WHERE action = 'attempt.extended' AND entity_id = $1::uuid`, attempt)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var reason, window string
		if err := rows.Scan(&reason, &window); err != nil {
			t.Fatal(err)
		}
		out[reason] = window
	}
	return out
}

// F1, "Maintenance treats a flexible attempt by its close": an extension that
// moves a flexible assignment's close into a window already scheduled moves
// its attempts in progress past the window, as scheduling the window after
// the extension would have, and audits that move apart; a strict attempt is
// moved as before, by its rule alone.
func TestAnExtensionIntoAScheduledWindowMovesAFlexibleAttemptPastIt(t *testing.T) {
	w := newReachWorld(t)
	ctx := context.Background()
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

	for _, id := range []string{flexible, strict} {
		req := as(w.a, false)
		req.ID = id
		if _, err := w.store.Extend(ctx, req, 30, false, time.Now()); err != nil {
			t.Fatal(err)
		}
	}

	if got, want := w.deadlineOf(t, flexibleAttempt), w.closesAt(t, flexible).Add(time.Hour); !got.Equal(want) {
		t.Errorf("the flexible attempt has deadline %v after its close moved into the window, want the close plus the window's hour, %v", got, want)
	}
	if moves := w.movesOf(t, flexibleAttempt); moves["assignment_extended"] != "" || moves["maintenance"] != window || len(moves) != 2 {
		t.Errorf("the flexible attempt's moves are audited as %v, want one for the extension and one for window %s", moves, window)
	}
	if got, want := w.deadlineOf(t, strictAttempt), w.startedOf(t, strictAttempt).Add(45*time.Minute); !got.Equal(want) {
		t.Errorf("the strict attempt has deadline %v, want its start plus 45 minutes, %v, as before", got, want)
	}
	if moves := w.movesOf(t, strictAttempt); len(moves) != 1 || moves["maintenance"] != "" {
		t.Errorf("the strict attempt's moves are audited as %v, want the extension's alone", moves)
	}
}

// An override whose close falls inside a window already scheduled moves the
// student's flexible attempt past the window with it; the other students'
// attempts stay.
func TestAnOverrideIntoAScheduledWindowMovesAFlexibleAttemptPastIt(t *testing.T) {
	w := newReachWorld(t)
	ctx := context.Background()
	base := time.Now().Truncate(time.Second)
	w.windowAhead(t, base.Add(40*time.Minute), base.Add(100*time.Minute))

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
}
