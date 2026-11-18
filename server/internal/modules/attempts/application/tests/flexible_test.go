//go:build integration

package application_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"quizzivy/internal/modules/attempts/application/command"
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
