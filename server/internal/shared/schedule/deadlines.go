package schedule

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// WindowLockKey is the maintenance-windows key in the 73819 advisory-lock
// namespace (docs/plan/20-data-model.md §28). The operator's window-schedule
// takes it exclusively; an attempt start, and a write to an assignment's window
// or overrides, take it shared.
const WindowLockKey = 40

// Execer is what the kernel's statements run on: a transaction.
type Execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// LockWindows takes the maintenance-windows advisory lock shared, for the rest
// of tx. A writer of an assignment's window calls it before it locks the
// assignment row, the order an attempt start keeps, so that the operator's
// window-schedule, which takes the lock exclusively and then updates attempts
// and assignments in one statement, never waits on a row that a writer holds
// while the writer waits on a row the schedule holds.
func LockWindows(ctx context.Context, tx Execer) error {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock_shared(73819, $1)`, WindowLockKey); err != nil {
		return fmt.Errorf("schedule: lock maintenance windows: %w", err)
	}
	return nil
}

// Recompute names the attempts in progress whose deadlines a change to a
// window may lengthen: those on AssignmentID, or only those of StudentIDs when
// it is not empty. Cause is a short word for the audit entry of each attempt
// moved, which is written as ActorID, with IP and UserAgent, at Now.
type Recompute struct {
	AssignmentID string
	StudentIDs   []string
	ActorID      string
	IP           string
	UserAgent    string
	Cause        string
	Now          time.Time
}

// RecomputeDeadlines moves the deadline of each attempt in progress that the
// window now lets run longer, and returns how many it moved. An attempt's
// deadline is the earlier of its start plus its student's time limit and its
// student's close, or that close alone under a flexible assignment (Mode.
// Deadline, the rule Rules.Deadline applies at the start); it is moved
// only when that is later than the deadline it has, so a deadline never
// shortens and one a maintenance window pushed past the rule stays. An attempt
// whose deadline has already passed, on the database's clock, is over whether
// or not it has been swept, and is not revived. The student's own override
// counts in both terms, so a student whose override closes later than the
// assignment keeps it.
//
// A flexible attempt is then moved through every maintenance window ahead
// that was already scheduled, as scheduling that window moved the attempts in
// progress then (FlexibleWindowDeadline), so that a close a writer puts inside
// a scheduled window gives the attempt the deadline it would have had if the
// window had been scheduled after the write; that move is audited apart, with
// the reason maintenance and the window's id. A strict attempt is not moved
// through windows: a strict start is refused instead when its deadline would
// meet one.
//
// tx must be the transaction that changed the window, after it took the
// maintenance-windows lock (LockWindows) and then the assignment row. Each
// attempt row is locked as it is read, so a concurrent submit, expiry or
// resume of the same attempt finishes before the deadline moves or sees the
// new one, and an attempt that was handed in meanwhile is skipped. The lock
// order is the windows lock, the assignment row, then its attempts. The count
// is of attempts moved, each once however many moves it took.
func RecomputeDeadlines(ctx context.Context, tx Execer, in Recompute) (int, error) {
	var students any
	if len(in.StudentIDs) > 0 {
		students = in.StudentIDs
	}
	var moved []string
	err := tx.QueryRow(ctx, `
		WITH live AS MATERIALIZED (
		  SELECT at.id, `+DeadlineOf("o")+` AS next_deadline
		    FROM app.attempts at
		    JOIN app.assignments a ON a.id = at.assignment_id
		    `+OverrideJoin("at.student_id")+`
		   WHERE at.assignment_id = $1::uuid AND at.status = 'in_progress'
		     AND at.deadline_at > now()
		     AND ($2::uuid[] IS NULL OR at.student_id = ANY($2::uuid[]))
		   ORDER BY at.id
		     FOR UPDATE OF at
		), moved AS (
		  UPDATE app.attempts at
		     SET deadline_at = live.next_deadline
		    FROM live
		   WHERE at.id = live.id AND live.next_deadline > at.deadline_at
		  RETURNING at.id, old.deadline_at AS prev_deadline, new.deadline_at AS next_deadline
		), logged AS (
		  INSERT INTO app.audit_log
		         (actor_user_id, action, entity, entity_id, occurred_at, ip, user_agent, diff)
		  SELECT nullif($3, '')::uuid, 'attempt.extended', 'attempt', moved.id, $4, nullif($5, '')::inet, nullif($6, ''),
		         jsonb_build_object(
		           'deadline_at', jsonb_build_object('old', moved.prev_deadline, 'new', moved.next_deadline),
		           'reason', $7::text, 'assignmentId', $1::uuid)
		    FROM moved
		)
		SELECT coalesce(array_agg(id::text), '{}') FROM moved`,
		in.AssignmentID, students, in.ActorID, in.Now, in.IP, in.UserAgent, in.Cause).Scan(&moved)
	if err != nil {
		return 0, fmt.Errorf("schedule: recompute deadlines: %w", err)
	}
	through, err := moveThroughWindows(ctx, tx, in, students)
	if err != nil {
		return 0, err
	}
	seen := make(map[string]struct{}, len(moved)+len(through))
	for _, id := range append(moved, through...) {
		seen[id] = struct{}{}
	}
	return len(seen), nil
}
