package jobs

import (
	"context"
	"log/slog"
	"time"

	"quizzivy/internal/platform/email"
)

// EmailDispatcher sends what the outbox holds. Dispatch handles at most one
// batch; NextDue answers when the earliest pending row falls due.
type EmailDispatcher interface {
	Dispatch(context.Context) (email.Batch, error)
	NextDue(context.Context) (time.Time, bool, error)
}

// EmailSchedule says how long RunEmail waits between batches. Wake carries
// this process's enqueues. Retry is the pause after a failed batch and Busy
// the pause when another process held the dispatch lock. Floor is the
// shortest wait for a row falling due, so a timer never spins. Idle is the
// longest wait of all, which bounds how long a row queued by a process that
// cannot fire Wake (the maintenance console) waits; zero leaves the wait
// unbounded.
type EmailSchedule struct {
	Wake        <-chan struct{}
	Retry, Busy time.Duration
	Floor, Idle time.Duration
}

// RunEmail dispatches once at start-up, again whenever Wake fires, whenever
// the earliest pending row falls due, and at least once per Idle, until ctx
// ends. Without Idle a timer is armed only while a row is pending, so an
// empty outbox never wakes the database.
func RunEmail(ctx context.Context, logger *slog.Logger, d EmailDispatcher, s EmailSchedule) {
	for ctx.Err() == nil {
		batch, err := d.Dispatch(ctx)
		var wait time.Duration
		armed := true
		switch {
		case ctx.Err() != nil:
			return
		case err != nil:
			logger.Warn("email dispatch failed", "code", "EMAIL_DISPATCH_FAILED")
			wait = s.Retry
		case batch.Busy:
			wait = s.Busy
		case batch.Claimed > 0:
			continue
		default:
			wait, armed, err = nextWait(ctx, d, s.Floor, s.Idle)
			if err != nil {
				logger.Warn("email dispatch failed", "code", "EMAIL_DISPATCH_FAILED")
				wait, armed = s.Retry, true
			}
		}
		if !emailSleep(ctx, s.Wake, wait, armed) {
			return
		}
	}
}

// nextWait is how long RunEmail sleeps after a batch that left nothing due:
// until the earliest pending row falls due, but no shorter than floor and,
// when idle is set, no longer than idle, so a row another process queued
// sooner than that is found at the latest one idle later.
func nextWait(ctx context.Context, d EmailDispatcher, floor, idle time.Duration) (time.Duration, bool, error) {
	due, pending, err := d.NextDue(ctx)
	switch {
	case err != nil:
		return 0, false, err
	case !pending:
		return idle, idle > 0, nil
	case idle > 0:
		return min(max(time.Until(due), floor), idle), true, nil
	}
	return max(time.Until(due), floor), true, nil
}

func emailSleep(ctx context.Context, wake <-chan struct{}, d time.Duration, armed bool) bool {
	var timer <-chan time.Time
	if armed {
		t := time.NewTimer(d)
		defer t.Stop()
		timer = t.C
	}
	select {
	case <-ctx.Done():
		return false
	case <-wake:
	case <-timer:
	}
	return true
}
