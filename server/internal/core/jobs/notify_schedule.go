package jobs

import (
	"context"
	"log/slog"
	"time"

	notificationsapp "quizzivy/internal/modules/notifications/application"
	notificationscommand "quizzivy/internal/modules/notifications/application/command"
	notificationsquery "quizzivy/internal/modules/notifications/application/query"
)

// NotifyScheduler is the work of the notification scheduler (T-R7.14): Run
// writes every email and weekly summary due by now, and reports busy when
// another machine's run held the lock; NextDue answers the next moment after
// now that has anything to write.
type NotifyScheduler interface {
	Run(ctx context.Context, now time.Time) (busy bool, err error)
	NextDue(ctx context.Context, now time.Time) (time.Time, bool, error)
}

// NotifySchedule says how RunNotifySchedule waits. Nudge carries this
// process's assignment writes. Idle is the longest sleep, when nothing is
// due sooner; Retry the pause after a failed run or read; Busy the pause
// when another machine held the lock, after which this one runs again in
// case that run began before a write it should have seen; Floor the
// shortest wait for a moment falling due, so a timer never spins. Now is the
// clock, time.Now when nil.
type NotifySchedule struct {
	Nudge                    <-chan struct{}
	Idle, Retry, Busy, Floor time.Duration
	Now                      func() time.Time
}

// RunNotifySchedule runs the scheduler at start-up, then again when the
// next moment it reads falls due, when Idle has passed, or when Nudge fires,
// until ctx ends. A failure is logged with its code only, and retried.
func RunNotifySchedule(ctx context.Context, logger *slog.Logger, s NotifyScheduler, schedule NotifySchedule) {
	now := schedule.Now
	if now == nil {
		now = time.Now
	}
	for ctx.Err() == nil {
		wait := schedule.next(ctx, logger, s, now)
		if ctx.Err() != nil || !scheduleSleep(ctx, schedule.Nudge, wait) {
			return
		}
	}
}

func (schedule NotifySchedule) next(ctx context.Context, logger *slog.Logger, s NotifyScheduler, now func() time.Time) time.Duration {
	busy, err := s.Run(ctx, now())
	switch {
	case ctx.Err() != nil:
		return 0
	case err != nil:
		logger.Warn("notification schedule failed", "code", "NOTIFY_SCHEDULE_FAILED")
		return schedule.Retry
	case busy:
		return schedule.Busy
	}
	at := now()
	due, ok, err := s.NextDue(ctx, at)
	switch {
	case ctx.Err() != nil:
		return 0
	case err != nil:
		logger.Warn("notification schedule failed", "code", "NOTIFY_SCHEDULE_FAILED")
		return schedule.Retry
	case !ok:
		return schedule.Idle
	}
	return min(max(due.Sub(at), schedule.Floor), schedule.Idle)
}

func scheduleSleep(ctx context.Context, nudge <-chan struct{}, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-nudge:
	case <-t.C:
	}
	return true
}

// Nudge is the in-process signal assignment writes send the notification
// scheduler (the assignments module's ScheduleNudge port). Signals coalesce,
// and Nudge never blocks.
type Nudge struct {
	c chan struct{}
}

// NewNudge is a nudge no write has sent yet.
func NewNudge() *Nudge {
	return &Nudge{c: make(chan struct{}, 1)}
}

// Nudge tells the scheduler that a close may have moved.
func (n *Nudge) Nudge() {
	select {
	case n.c <- struct{}{}:
	default:
	}
}

// C is the channel Nudge signals on, for RunNotifySchedule.
func (n *Nudge) C() <-chan struct{} { return n.c }

// NotificationSchedule is the notifications module's scheduler: its two
// scheduled commands, each under the scheduler's lock, and its NextDue.
// Each run is bounded by budget, and one that wrote anything says so on
// logger.
func NotificationSchedule(app *notificationsapp.Application, budget time.Duration, logger *slog.Logger) NotifyScheduler {
	return notificationSchedule{app: app, budget: budget, logger: logger}
}

type notificationSchedule struct {
	app    *notificationsapp.Application
	budget time.Duration
	logger *slog.Logger
}

func (n notificationSchedule) Run(ctx context.Context, now time.Time) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, n.budget)
	defer cancel()
	due, err := n.app.Commands.SendDueEmails.Handle(ctx, notificationscommand.SendDueEmails{Now: now})
	if err != nil {
		return false, err
	}
	weekly, err := n.app.Commands.SendWeeklySummary.Handle(ctx, notificationscommand.SendWeeklySummary{Now: now})
	if err != nil {
		return false, err
	}
	if written := due.Emails + weekly.Emails + weekly.InApp; written > 0 {
		n.logger.Info("notification schedule wrote", "emails", due.Emails+weekly.Emails, "in_app", weekly.InApp)
	}
	return due.Busy || weekly.Busy, nil
}

func (n notificationSchedule) NextDue(ctx context.Context, now time.Time) (time.Time, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, n.budget)
	defer cancel()
	due, err := n.app.Queries.NextDue.Handle(ctx, notificationsquery.NextDue{Now: now})
	return due.At, due.Any, err
}
