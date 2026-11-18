//go:build integration

package application_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"quizzivy/internal/modules/attempts/application/command"
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
