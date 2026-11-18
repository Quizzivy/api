package schedule

import (
	"context"
	"fmt"
	"time"
)

// FlexibleWindowDeadline is the deadline a maintenance window from $1 to $2
// gives a flexible attempt aliased a (its deadline_at, assignment_id and
// student_id), on its assignment aliased s, when the deadline is after the
// window starts: the latest of the deadline, moved by the window's length when
// it falls inside the window; the assignment's close, moved so when it falls
// inside the window and was not closed early; and the student's override
// close, moved so when it falls inside the window. It is never earlier than
// the deadline. Scheduling a window applies it to every flexible attempt in
// progress (core/maintenance, ScheduleWindow); after the window was scheduled,
// ThroughWindows applies it to an attempt that starts, RecomputeDeadlines to
// the attempts whose deadline an assignment-level writer (Extend, Update,
// Reopen, SetOverrides) moves, and MoveAttemptThroughWindows to an attempt a
// teacher extends by hand, so that a flexible attempt ends where it would have
// if it had been running when the window was scheduled.
const FlexibleWindowDeadline = `greatest(
       a.deadline_at + CASE WHEN a.deadline_at < $2::timestamptz
                            THEN $2::timestamptz - $1::timestamptz ELSE interval '0' END,
       least(s.closed_at, s.closes_at + CASE WHEN s.closed_at IS NULL
                                              AND s.closes_at >= $1::timestamptz AND s.closes_at < $2::timestamptz
                                             THEN $2::timestamptz - $1::timestamptz ELSE interval '0' END),
       (SELECT o.closes_at + CASE WHEN o.closes_at >= $1::timestamptz AND o.closes_at < $2::timestamptz
                                  THEN $2::timestamptz - $1::timestamptz ELSE interval '0' END
          FROM app.assignment_student_overrides o
         WHERE o.assignment_id = a.assignment_id AND o.student_id = a.student_id))`

// ScheduledWindow is one maintenance window that was not cancelled.
type ScheduledWindow struct {
	ID       string
	StartsAt time.Time
	EndsAt   time.Time
}

// WindowsAhead returns the windows that were not cancelled and end after
// after, or after the database's clock when after is nil, earliest first. tx
// must hold the maintenance-windows lock (LockWindows, or the attempt start's
// guard), so that no window is scheduled, cancelled or ended until it ends.
func WindowsAhead(ctx context.Context, tx Execer, after *time.Time) ([]ScheduledWindow, error) {
	var (
		ids          []string
		starts, ends []time.Time
	)
	err := tx.QueryRow(ctx, `
		SELECT coalesce(array_agg(id::text ORDER BY starts_at), '{}'),
		       coalesce(array_agg(starts_at ORDER BY starts_at), '{}'),
		       coalesce(array_agg(ends_at ORDER BY starts_at), '{}')
		  FROM app.maintenance_windows
		 WHERE cancelled_at IS NULL AND ends_at > coalesce($1::timestamptz, now())`, after).Scan(&ids, &starts, &ends)
	if err != nil {
		return nil, fmt.Errorf("schedule: read the windows ahead: %w", err)
	}
	out := make([]ScheduledWindow, len(ids))
	for i := range ids {
		out[i] = ScheduledWindow{ID: ids[i], StartsAt: starts[i], EndsAt: ends[i]}
	}
	return out, nil
}

// FlexibleStart names the attempt a student starts on a flexible assignment,
// before it is written: its deadline as the rule gives it (Mode.Deadline, the
// student's close) and the moment it starts.
type FlexibleStart struct {
	AssignmentID string
	StudentID    string
	Deadline     time.Time
	StartedAt    time.Time
}

// ThroughWindows is the deadline of a flexible attempt that starts as in
// describes, once every window ahead of it has moved it as scheduling that
// window would have moved it had the attempt been running then
// (FlexibleWindowDeadline), the earliest window first. A window under way
// refuses the start before this runs (the start guard), so only windows ahead
// move it. tx must hold the maintenance-windows lock, as WindowsAhead says.
func ThroughWindows(ctx context.Context, tx Execer, in FlexibleStart) (time.Time, error) {
	windows, err := WindowsAhead(ctx, tx, &in.StartedAt)
	if err != nil {
		return time.Time{}, err
	}
	deadline := in.Deadline
	for _, w := range windows {
		if !deadline.After(w.StartsAt) {
			continue
		}
		err := tx.QueryRow(ctx, `
			SELECT `+FlexibleWindowDeadline+`
			  FROM (SELECT $3::timestamptz AS deadline_at, $4::uuid AS assignment_id, $5::uuid AS student_id) a
			  JOIN app.assignments s ON s.id = a.assignment_id`,
			w.StartsAt, w.EndsAt, deadline, in.AssignmentID, in.StudentID).Scan(&deadline)
		if err != nil {
			return time.Time{}, fmt.Errorf("schedule: move a flexible start through window %s: %w", w.ID, err)
		}
	}
	return deadline, nil
}

// moveThroughWindows moves, for each window ahead, earliest first, the
// deadline of each flexible attempt in progress on in's assignment that
// students and attempt name (nil names every one) and that the window lets
// run longer (FlexibleWindowDeadline), and audits each move as in's actor
// with the reason maintenance and the window's id. It returns the ids of the
// attempts it moved.
func moveThroughWindows(ctx context.Context, tx Execer, in Recompute, students, attempt any) ([]string, error) {
	windows, err := WindowsAhead(ctx, tx, nil)
	if err != nil {
		return nil, err
	}
	var moved []string
	for _, w := range windows {
		var ids []string
		err := tx.QueryRow(ctx, `
			WITH moved AS (
			  UPDATE app.attempts a
			     SET deadline_at = `+FlexibleWindowDeadline+`
			    FROM app.assignments s
			   WHERE s.id = a.assignment_id AND s.mode = 'flexible'
			     AND a.assignment_id = $3::uuid AND a.status = 'in_progress'
			     AND a.deadline_at > now() AND a.deadline_at > $1::timestamptz
			     AND ($4::uuid[] IS NULL OR a.student_id = ANY($4::uuid[]))
			     AND ($11::uuid IS NULL OR a.id = $11::uuid)
			     AND `+FlexibleWindowDeadline+` > a.deadline_at
			  RETURNING a.id, old.deadline_at AS prev_deadline, new.deadline_at AS next_deadline
			), logged AS (
			  INSERT INTO app.audit_log
			         (actor_user_id, action, entity, entity_id, occurred_at, ip, user_agent, diff)
			  SELECT nullif($5, '')::uuid, 'attempt.extended', 'attempt', moved.id, $6, nullif($7, '')::inet, nullif($8, ''),
			         jsonb_build_object(
			           'deadline_at', jsonb_build_object('old', moved.prev_deadline, 'new', moved.next_deadline),
			           'reason', 'maintenance', 'windowId', $9::uuid, 'cause', $10::text, 'assignmentId', $3::uuid)
			    FROM moved
			)
			SELECT coalesce(array_agg(id::text), '{}') FROM moved`,
			w.StartsAt, w.EndsAt, in.AssignmentID, students, in.ActorID, in.Now, in.IP, in.UserAgent, w.ID, in.Cause, attempt).Scan(&ids)
		if err != nil {
			return nil, fmt.Errorf("schedule: move deadlines through window %s: %w", w.ID, err)
		}
		moved = append(moved, ids...)
	}
	return moved, nil
}

// AttemptMove names one attempt in progress whose deadline a teacher moved by
// hand (the attempts module's Extend), on AssignmentID. Cause is the short
// word for the audit entry of a move through a window, written as ActorID,
// with IP and UserAgent, at Now.
type AttemptMove struct {
	AttemptID    string
	AssignmentID string
	ActorID      string
	IP           string
	UserAgent    string
	Cause        string
	Now          time.Time
}

// MoveAttemptThroughWindows moves the one attempt in names, when its
// assignment is flexible, through every maintenance window ahead that was
// already scheduled, as RecomputeDeadlines moves the attempts of an
// assignment-level writer, so that a teacher's extension of that attempt into
// a scheduled window is kept past the window as scheduling the window after
// the extension would have kept it. It reports whether the attempt moved. A
// strict attempt is not moved, as RecomputeDeadlines does not move one. tx
// must hold the maintenance-windows lock (LockWindows), taken before the
// attempt row.
func MoveAttemptThroughWindows(ctx context.Context, tx Execer, in AttemptMove) (bool, error) {
	moved, err := moveThroughWindows(ctx, tx, Recompute{
		AssignmentID: in.AssignmentID, ActorID: in.ActorID, IP: in.IP, UserAgent: in.UserAgent, Cause: in.Cause, Now: in.Now,
	}, nil, in.AttemptID)
	if err != nil {
		return false, err
	}
	return len(moved) > 0, nil
}
