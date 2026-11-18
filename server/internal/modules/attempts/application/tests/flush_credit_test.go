//go:build integration

package application_test

import (
	"testing"

	"quizzivy/internal/modules/attempts/application/command"
	notificationsdomain "quizzivy/internal/modules/notifications/domain"
)

// A flush whose credit of time on the paper fails writes nothing: the flag
// its absence set is taken back with it, so the retry flags the paper again
// and the teacher is told once, under the flag's key.
func TestAFlushWhoseCreditFailsKeepsItsFlagForTheRetry(t *testing.T) {
	pool := newPool(t)
	w := flexibleWorld(t, pool, focusLimit(3, "flag"))
	tell := &told{}
	svc := announcing(pool, tell, nil)
	session := startFor(t, svc, w, w.student)
	// active_ms at bigint's top makes the credit's addition fail after the
	// events and the flag were written.
	if _, err := pool.Exec(t.Context(), `UPDATE app.attempts SET active_ms = 9223372036854775807 WHERE id = $1::uuid`, session.Attempt.ID); err != nil {
		t.Fatal(err)
	}
	ms := 1_000
	in := flushEvents(w, session, away(1, 4000))
	in.ActiveMs = &ms
	if _, err := svc.Commands.Flush.Handle(t.Context(), command.Flush{Input: in}); err == nil {
		t.Fatal("a flush whose credit overflows succeeded")
	}
	if n, flagged := focusState(t, pool, session.Attempt.ID); n != 0 || flagged {
		t.Errorf("the failed flush left count %d flagged %v, want nothing written", n, flagged)
	}

	if _, err := pool.Exec(t.Context(), `UPDATE app.attempts SET active_ms = 0 WHERE id = $1::uuid`, session.Attempt.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Commands.Flush.Handle(t.Context(), command.Flush{Input: in}); err != nil {
		t.Fatal(err)
	}
	if n, flagged := focusState(t, pool, session.Attempt.ID); n != 1 || !flagged {
		t.Errorf("after the retry: count %d flagged %v, want 1 and flagged", n, flagged)
	}
	if sent := tell.of(notificationsdomain.AttemptFlagged); len(sent) != 1 || sent[0].DedupeKey != notificationsdomain.FlaggedKey(session.Attempt.ID) {
		t.Errorf("the failed flush and its retry sent %+v, want one notice under the flag's key", sent)
	}
}
