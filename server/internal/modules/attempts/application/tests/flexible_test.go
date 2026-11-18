//go:build integration

package application_test

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"quizzivy/internal/modules/attempts/application"
	"quizzivy/internal/modules/attempts/application/command"
	"quizzivy/internal/modules/attempts/application/query"
	"quizzivy/internal/modules/attempts/domain"
	notificationsdomain "quizzivy/internal/modules/notifications/domain"
	"quizzivy/internal/shared/schedule"
)

// flexibleWorld is seedWorld's assignment under F1's flexible mode.
func flexibleWorld(t *testing.T, pool *pgxpool.Pool, o worldOpts) world {
	t.Helper()
	w := seedWorld(t, pool, o)
	if _, err := pool.Exec(context.Background(), `UPDATE app.assignments SET mode = 'flexible' WHERE id = $1::uuid`, w.assignment); err != nil {
		t.Fatal(err)
	}
	return w
}

func activeOf(t *testing.T, pool *pgxpool.Pool, attemptID string) (*int64, *time.Time) {
	t.Helper()
	var ms *int64
	var at *time.Time
	if err := pool.QueryRow(context.Background(), `SELECT active_ms, active_reported_at FROM app.attempts WHERE id = $1::uuid`, attemptID).Scan(&ms, &at); err != nil {
		t.Fatal(err)
	}
	return ms, at
}

// msText prints a time on the paper read by activeOf.
func msText(ms *int64) string {
	if ms == nil {
		return "NULL"
	}
	return strconv.FormatInt(*ms, 10)
}

func statusOf(t *testing.T, pool *pgxpool.Pool, attemptID string) string {
	t.Helper()
	var status string
	if err := pool.QueryRow(context.Background(), `SELECT status::text FROM app.attempts WHERE id = $1::uuid`, attemptID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	return status
}

func TestAFlexibleAttemptRunsToTheStudentsCloseAndSaysItsMode(t *testing.T) {
	pool := newPool(t)
	w := flexibleWorld(t, pool, openAssignment())
	svc := newService(t, pool)
	session := startFor(t, svc, w, w.student)
	var closes time.Time
	if err := pool.QueryRow(t.Context(), `SELECT closes_at FROM app.assignments WHERE id = $1::uuid`, w.assignment).Scan(&closes); err != nil {
		t.Fatal(err)
	}
	if !session.Attempt.DeadlineAt.Equal(closes) || session.Mode != schedule.Flexible {
		t.Errorf("deadline %v and mode %q, want the close %v, not the 60-minute limit, and flexible", session.Attempt.DeadlineAt, session.Mode, closes)
	}
	strict := seedWorld(t, pool, openAssignment())
	if got := startFor(t, svc, strict, strict.student); got.Mode != schedule.Strict {
		t.Errorf("a strict session reads %q", got.Mode)
	}

	extended, err := svc.Commands.Extend.Handle(t.Context(), command.Extend{Request: teacher(w), AttemptID: session.Attempt.ID, Minutes: 30, Reason: "Mất điện"})
	if err != nil || !extended.DeadlineAt.Equal(closes.Add(30*time.Minute)) {
		t.Errorf("extending a flexible attempt: %v, deadline %v, want 30 minutes past the close", err, extended.DeadlineAt)
	}
}

// Under flexible the first counted absence flags the paper whatever the
// stored rule says (here unlimited, warn), and no absence closes it, on a
// save or on a submit that claims auto_submit.
func TestAFlexibleAbsenceFlagsAndNeverHandsIn(t *testing.T) {
	pool := newPool(t)
	w := flexibleWorld(t, pool, focusLimit(0, "warn"))
	svc := newService(t, pool)
	session := startFor(t, svc, w, w.student)
	if _, err := svc.Commands.Flush.Handle(t.Context(), command.Flush{Input: flushEvents(w, session, away(1, 4000))}); err != nil {
		t.Fatal(err)
	}
	if n, flagged := focusState(t, pool, session.Attempt.ID); n != 1 || !flagged {
		t.Fatalf("after one absence: count %d flagged %v, want 1 and flagged", n, flagged)
	}

	if _, err := pool.Exec(t.Context(), `UPDATE app.assignments SET integrity_max_focus_loss = -1, integrity_on_limit_exceeded = 'auto_submit' WHERE id = $1`, w.assignment); err != nil {
		t.Fatal(err)
	}
	in := domain.SaveInput{AttemptID: session.Attempt.ID, StudentID: w.student, SessionID: session.SessionID, Events: []domain.Event{away(2, 4000)}}
	if _, err := svc.Commands.Save.Handle(t.Context(), command.Save{Input: in}); err != nil {
		t.Fatal(err)
	}
	if status := statusOf(t, pool, session.Attempt.ID); status != "in_progress" {
		t.Fatalf("a save past an auto_submit rule left the flexible paper %s", status)
	}
	if _, err := svc.Commands.Submit.Handle(t.Context(), command.Submit{AttemptID: session.Attempt.ID, StudentID: w.student, Reason: domain.AutoSubmit}); err != nil {
		t.Fatal(err)
	}
	if status := statusOf(t, pool, session.Attempt.ID); status != "submitted" || containsKind(eventKinds(t, pool, session.Attempt.ID), "auto_submit") {
		t.Errorf("the submit left %s with events %v, want submitted as a manual hand-in", status, eventKinds(t, pool, session.Attempt.ID))
	}
}

// The first flag tells the teacher under the flag's key; every later absence
// under flexible tells them again under the fifteen-minute bucket's key, with
// the new count, by a flush as by a save.
func TestAFlexibleAttemptTellsTheTeacherAgainOncePerBucket(t *testing.T) {
	pool := newPool(t)
	w := flexibleWorld(t, pool, focusLimit(3, "flag"))
	tell := &told{}
	svc := announcing(pool, tell, nil)
	session := startFor(t, svc, w, w.student)
	flush := func(seq int) {
		t.Helper()
		if _, err := svc.Commands.Flush.Handle(t.Context(), command.Flush{Input: flushEvents(w, session, away(seq, 4000))}); err != nil {
			t.Fatal(err)
		}
	}
	flush(1)
	sent := tell.of(notificationsdomain.AttemptFlagged)
	if len(sent) != 1 || sent[0].DedupeKey != notificationsdomain.FlaggedKey(session.Attempt.ID) {
		t.Fatalf("the first absence sent %+v, want one notice under the flag's key", sent)
	}
	flush(2)
	in := domain.SaveInput{AttemptID: session.Attempt.ID, StudentID: w.student, SessionID: session.SessionID, Events: []domain.Event{away(3, 4000)}}
	if _, err := svc.Commands.Save.Handle(t.Context(), command.Save{Input: in}); err != nil {
		t.Fatal(err)
	}
	flush(3) // a retried batch counts nothing new and tells nobody
	sent = tell.of(notificationsdomain.AttemptFlagged)
	if len(sent) != 3 {
		t.Fatalf("three absences and a retry sent %d notices, want 3: %+v", len(sent), sent)
	}
	for i, n := range sent[1:] {
		bucket, again := notificationsdomain.FlaggedAgainBucket(n.DedupeKey)
		if want := notificationsdomain.FlaggedKey(session.Attempt.ID) + ":" + bucket; !again || n.DedupeKey != want {
			t.Errorf("repeat %d is keyed %q, want a fifteen-minute bucket's key", i+1, n.DedupeKey)
		}
		if p, ok := n.Params.(notificationsdomain.Flagged); !ok || p.FocusLost != i+2 {
			t.Errorf("repeat %d carries %+v, want focusLost %d", i+1, n.Params, i+2)
		}
	}
}

// A strict paper writes what it wrote at v0.14.0: nothing under warn, and one
// notice at the flag however many absences follow.
func TestAStrictAttemptIsToldOfAsBefore(t *testing.T) {
	pool := newPool(t)
	for _, c := range []struct {
		rule string
		want int
	}{{"warn", 0}, {"flag", 1}} {
		w := seedWorld(t, pool, focusLimit(1, c.rule))
		tell := &told{}
		svc := announcing(pool, tell, nil)
		session := startFor(t, svc, w, w.student)
		for seq := 1; seq <= 4; seq++ {
			if _, err := svc.Commands.Flush.Handle(t.Context(), command.Flush{Input: flushEvents(w, session, away(seq, 4000))}); err != nil {
				t.Fatal(err)
			}
		}
		if sent := tell.of(notificationsdomain.AttemptFlagged); len(sent) != c.want {
			t.Errorf("strict %s sent %d notices, want %d: %+v", c.rule, len(sent), c.want, sent)
		}
	}
}

// Time on the paper: a start measures from zero; a report is capped at the
// time since the session's previous one plus five seconds; a superseded
// session's report is dropped; a row from before F1 stays unmeasured.
func TestTimeOnThePaperIsCappedAndComesOnlyFromTheCurrentSession(t *testing.T) {
	pool := newPool(t)
	w := seedWorld(t, pool, openAssignment())
	svc := announcing(pool, &told{}, nil)
	session := startFor(t, svc, w, w.student)
	if ms, at := activeOf(t, pool, session.Attempt.ID); ms == nil || *ms != 0 || at == nil || !at.Equal(session.Attempt.StartedAt) {
		t.Fatalf("a start measures %s from %v, want 0 from the start %v", msText(ms), at, session.Attempt.StartedAt)
	}
	report := func(s domain.Session, ms int) {
		t.Helper()
		in := domain.SaveInput{AttemptID: s.Attempt.ID, StudentID: w.student, SessionID: s.SessionID, ActiveMs: &ms}
		if _, err := svc.Commands.Save.Handle(t.Context(), command.Save{Input: in}); err != nil {
			t.Fatal(err)
		}
	}
	rewind := func(by time.Duration) {
		t.Helper()
		if _, err := pool.Exec(t.Context(), `UPDATE app.attempts SET active_reported_at = active_reported_at - make_interval(secs => $2) WHERE id = $1::uuid`,
			session.Attempt.ID, by.Seconds()); err != nil {
			t.Fatal(err)
		}
	}
	rewind(time.Minute)
	report(session, 20_000)
	if ms, _ := activeOf(t, pool, session.Attempt.ID); ms == nil || *ms != 20_000 {
		t.Fatalf("a report within the time since the start credited %s, want 20000", msText(ms))
	}
	rewind(10 * time.Second)
	report(session, 600_000)
	if ms, _ := activeOf(t, pool, session.Attempt.ID); ms == nil || *ms < 35_000 || *ms > 36_000 {
		t.Fatalf("a report of ten minutes, ten seconds after the last, brought the time to %s, want about 20000 + 10000 + 5000", msText(ms))
	}

	// The old session's last report is an hour back: the resume, not that
	// report, is what the new session's first report counts from.
	rewind(time.Hour)
	resumed, err := svc.Commands.StartOrResume.Handle(t.Context(), command.StartOrResume{AssignmentID: w.assignment, StudentID: w.student, Resume: session.Attempt.ID})
	if err != nil {
		t.Fatal(err)
	}
	before, at := activeOf(t, pool, session.Attempt.ID)
	if at == nil || time.Since(*at) > 5*time.Second || time.Until(*at) > 5*time.Second {
		t.Errorf("the resume left the previous report at %v, want it now", at)
	}
	report(resumed, 600_000)
	if ms, _ := activeOf(t, pool, session.Attempt.ID); ms == nil || *ms-*before > 6_000 {
		t.Errorf("the first report after the resume brought the time to %s from %d, want at most about five seconds more, not the hour since the old session's report", msText(ms), *before)
	}
	before, _ = activeOf(t, pool, session.Attempt.ID)
	rewind(time.Minute)
	stale := 30_000
	old := flushEvents(w, session)
	old.ActiveMs = &stale
	if _, err := svc.Commands.Flush.Handle(t.Context(), command.Flush{Input: old}); err != nil {
		t.Fatal(err)
	}
	if ms, _ := activeOf(t, pool, session.Attempt.ID); ms == nil || *ms != *before {
		t.Errorf("the superseded session's report moved the time to %s from %d", msText(ms), *before)
	}
	current := flushEvents(w, resumed)
	current.ActiveMs = &stale
	if _, err := svc.Commands.Flush.Handle(t.Context(), command.Flush{Input: current}); err != nil {
		t.Fatal(err)
	}
	if ms, _ := activeOf(t, pool, session.Attempt.ID); ms == nil || *ms != *before+30_000 {
		t.Errorf("the current session's flush brought the time to %s, want %d", msText(ms), *before+30_000)
	}

	monitorShows(t, svc, w, session.Attempt.ID, int((*before+30_000)/1000))
	if _, err := pool.Exec(t.Context(), `UPDATE app.attempts SET active_ms = NULL, active_reported_at = NULL WHERE id = $1::uuid`, session.Attempt.ID); err != nil {
		t.Fatal(err)
	}
	report(resumed, 1_000)
	if ms, at := activeOf(t, pool, session.Attempt.ID); ms != nil || at != nil {
		t.Errorf("a row from before F1 reads %s from %v after a report, want it unmeasured", msText(ms), at)
	}
	monitorShows(t, svc, w, session.Attempt.ID, -1)
}

// monitorShows checks the monitor's and the review's time on the paper, in
// whole seconds, or none when seconds is negative.
func monitorShows(t *testing.T, svc *application.Application, w world, attemptID string, seconds int) {
	t.Helper()
	monitor, err := svc.Queries.Monitor.Handle(t.Context(), query.Monitor{AssignmentID: w.assignment, Scope: everyone})
	if err != nil {
		t.Fatal(err)
	}
	review, err := svc.Queries.Review.Handle(t.Context(), query.Review{AttemptID: attemptID, Scope: everyone})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range monitor.Rows {
		if row.AttemptID == nil || *row.AttemptID != attemptID {
			continue
		}
		for name, got := range map[string]*int{"monitor": row.ActiveSeconds, "review": review.ActiveSeconds} {
			switch {
			case seconds < 0 && got != nil:
				t.Errorf("the %s shows %d seconds for an unmeasured attempt", name, *got)
			case seconds >= 0 && (got == nil || *got != seconds):
				t.Errorf("the %s shows %v seconds, want %d", name, got, seconds)
			}
		}
		return
	}
	t.Errorf("the monitor has no row for %s", attemptID)
}
