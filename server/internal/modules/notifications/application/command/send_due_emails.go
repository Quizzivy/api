package command

import (
	"context"
	"time"

	"quizzivy/internal/modules/notifications/application/internal/support"
	"quizzivy/internal/modules/notifications/domain"
)

// Scheduled is what one scheduled run did: Busy when another run held the
// scheduler's lock and this one did nothing, else how many emails and in-app
// items it wrote.
type Scheduled struct {
	Busy    bool
	Emails  int
	InApp   int
	Skipped int
}

// SendDueEmails enqueues, under the scheduler's lock, every reminder a day
// and an hour before a student's close, every warning an hour before an
// assignment's close to the teachers it concerns, and every result an
// after-close release showed, whose moment lies in
// (Now - domain.ScheduleLookback, Now] and whose email the outbox does not
// hold (domain.ScheduledRun.DueEmails). Each goes only while its
// recipient's switch lets it and NOTIFY_EMAIL_EVENTS leaves it on, under a
// dedupe key that names it, so a second run, here or on another machine,
// enqueues nothing. R4's in-app items for the same moments are written
// when their reader asks (MaterialiseDue), not here.
type SendDueEmails struct {
	Now time.Time
}

type SendDueEmailsHandler struct {
	*support.Service
}

func (s SendDueEmailsHandler) Handle(ctx context.Context, cmd SendDueEmails) (Scheduled, error) {
	if s.Schedule == nil {
		return Scheduled{}, domain.ErrNoSchedule
	}
	var done Scheduled
	locked, err := s.Schedule.Locked(ctx, func(run domain.ScheduledRun) error {
		return s.within(ctx, run, cmd.Now, &done)
	})
	if err != nil {
		return Scheduled{}, err
	}
	done.Busy = !locked
	return done, nil
}

// within is SendDueEmails's work inside run, a transaction that already
// holds the scheduler's lock, added to done.
func (s SendDueEmailsHandler) within(ctx context.Context, run domain.ScheduledRun, now time.Time, done *Scheduled) error {
	emails, err := run.DueEmails(ctx, now)
	if err != nil {
		return err
	}
	for _, e := range emails {
		if !s.EmailOn(e.Template) {
			done.Skipped++
			continue
		}
		wrote, err := run.Enqueue(ctx, e)
		if err != nil {
			return err
		}
		if wrote {
			done.Emails++
		}
	}
	return nil
}
