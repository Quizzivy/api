//go:build integration

package repositories_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"quizzivy/internal/modules/assignments/domain"
	"quizzivy/internal/modules/assignments/repositories"
	"quizzivy/internal/platform/db"
	"quizzivy/internal/shared/schedule"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func waitingOnARowLock(t *testing.T, pool *pgxpool.Pool, application string) bool {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		var waiting int
		if err := pool.QueryRow(context.Background(), `
			SELECT count(*) FROM pg_stat_activity
			 WHERE application_name = $1 AND wait_event_type = 'Lock' AND wait_event <> 'advisory'`, application).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting > 0 {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

// F1: a start that holds the assignment row shared while it inserts its
// attempt locks the mode for a change that waited on that row: the change
// sees the attempt once the row is its own, and is refused.
func TestAStartTheModeChangeWaitedOnLocksTheMode(t *testing.T) {
	pool := newPool(t)
	w := seedWorld(t, pool, "published")
	ctx := context.Background()
	in := legalInput(w)
	in.OpensAt, in.ClosesAt = time.Now().Add(time.Hour), time.Now().Add(5*time.Hour)
	created, err := repositories.NewPostgres(db.NewContext(pool)).Create(ctx, request(w), in)
	if err != nil {
		t.Fatal(err)
	}

	// The holder stands in for a start that passed its check at the open: it
	// holds the assignment row FOR SHARE, as attempts' Create does, and inserts.
	holder, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := holder.Rollback(context.Background()); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
			t.Errorf("releasing the start: %v", err)
		}
	})
	if _, err := holder.Exec(ctx, `SELECT 1 FROM app.assignments a WHERE a.id = $1::uuid FOR SHARE OF a`, created.ID); err != nil {
		t.Fatal(err)
	}
	var attempt string
	var deadline time.Time
	if err := holder.QueryRow(ctx, `
		INSERT INTO app.attempts
		  (assignment_id, test_version_id, student_id, attempt_no, status, session_id,
		   shuffle_seed, beacon_token_hash, started_at, deadline_at)
		VALUES ($1::uuid, $2::uuid, $3::uuid, 1, 'in_progress', gen_random_uuid(), 1, sha256('b'::bytea),
		        now(), now() + interval '45 minutes')
		RETURNING id::text, deadline_at`, created.ID, w.versionID, w.student).Scan(&attempt, &deadline); err != nil {
		t.Fatal(err)
	}

	application := "qv-mode-lock-" + nonce(t)
	store := repositories.NewPostgres(db.NewContext(poolNamed(t, application)))
	req := request(w)
	req.ID = created.ID
	in.Mode, in.Now = schedule.Flexible, time.Now()
	finished := make(chan error, 1)
	go func() {
		_, err := store.Update(ctx, req, in)
		finished <- err
	}()
	if !waitingOnARowLock(t, pool, application) {
		t.Fatal("the mode change never waited on the row the start holds")
	}
	if err := holder.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-finished:
		if !errors.Is(err, domain.ErrModeLocked) {
			t.Errorf("a mode change that waited on a start: %v, want ErrModeLocked", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the mode change never finished after the start committed")
	}

	var mode string
	var after time.Time
	if err := pool.QueryRow(ctx, `
		SELECT s.mode::text, a.deadline_at FROM app.assignments s JOIN app.attempts a ON a.assignment_id = s.id
		 WHERE a.id = $1::uuid`, attempt).Scan(&mode, &after); err != nil {
		t.Fatal(err)
	}
	if mode != string(schedule.Strict) || !after.Equal(deadline) {
		t.Errorf("after the race the mode is %s and the attempt's deadline %v, want strict and %v", mode, after, deadline)
	}
}
