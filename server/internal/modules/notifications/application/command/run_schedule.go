package command

import (
	"context"
	"time"

	"quizzivy/internal/modules/notifications/application/internal/support"
	"quizzivy/internal/modules/notifications/domain"
)

// RunSchedule is one run of the notification scheduler (T-R7.14): the work
// of SendDueEmails and then of SendWeeklySummary, as of Now, in one
// transaction that takes the scheduler's lock once and holds it until both
// are written. While another run holds the lock it writes nothing and is
// Busy. Emails, InApp and Skipped are the two halves' together.
type RunSchedule struct {
	Now time.Time
}

type RunScheduleHandler struct {
	*support.Service
}

func (s RunScheduleHandler) Handle(ctx context.Context, cmd RunSchedule) (Scheduled, error) {
	if s.Schedule == nil {
		return Scheduled{}, domain.ErrNoSchedule
	}
	var done Scheduled
	locked, err := s.Schedule.Locked(ctx, func(run domain.ScheduledRun) error {
		if err := SendDueEmailsHandler(s).within(ctx, run, cmd.Now, &done); err != nil {
			return err
		}
		return SendWeeklySummaryHandler(s).within(ctx, run, cmd.Now, &done)
	})
	if err != nil {
		return Scheduled{}, err
	}
	done.Busy = !locked
	return done, nil
}
