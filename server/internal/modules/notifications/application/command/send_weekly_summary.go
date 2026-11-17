package command

import (
	"context"
	"time"

	"quizzivy/internal/modules/notifications/application/internal/support"
	"quizzivy/internal/modules/notifications/domain"
)

// SendWeeklySummary writes, under the scheduler's lock, each teacher's
// summary of the week before once Monday 07:00 has come in their own zone,
// for as long as that moment lies in (Now - domain.ScheduleLookback, Now]:
// the email while their switch's email column is on and NOTIFY_EMAIL_EVENTS
// leaves it on, and the in-app item while its in-app column is on (off by
// default). A teacher whose active classes had no paper handed in and no
// attempt flagged that week gets neither. The email's key names the
// teacher and the ISO week, and the item's the week, so a second run writes
// nothing.
type SendWeeklySummary struct {
	Now time.Time
}

type SendWeeklySummaryHandler struct {
	*support.Service
}

func (s SendWeeklySummaryHandler) Handle(ctx context.Context, cmd SendWeeklySummary) (Scheduled, error) {
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

// within is SendWeeklySummary's work inside run, a transaction that already
// holds the scheduler's lock, added to done.
func (s SendWeeklySummaryHandler) within(ctx context.Context, run domain.ScheduledRun, now time.Time, done *Scheduled) error {
	teachers, err := run.WeeklyTeachers(ctx, domain.WeeklyOn{Email: s.EmailOn(domain.EmailWeeklySummary)})
	if err != nil {
		return err
	}
	zones := map[string]*time.Location{}
	for _, teacher := range teachers {
		zone, seen := zones[teacher.Zone]
		if !seen {
			zone, _ = domain.Zone(teacher.Zone)
			zones[teacher.Zone] = zone
		}
		if zone == nil {
			done.Skipped++
			continue
		}
		if err := s.summarise(ctx, run, teacher, zone, now, done); err != nil {
			return err
		}
	}
	return nil
}

func (s SendWeeklySummaryHandler) summarise(ctx context.Context, run domain.ScheduledRun, teacher domain.WeeklyTeacher, zone *time.Location, now time.Time, done *Scheduled) error {
	week, due := domain.LastWeek(now, zone)
	if !due.After(now.Add(-domain.ScheduleLookback)) {
		return nil
	}
	emailHeld, itemHeld, err := run.WeeklyHeld(ctx, teacher.ID, week)
	if err != nil {
		return err
	}
	teacher.Email, teacher.InApp = teacher.Email && !emailHeld, teacher.InApp && !itemHeld
	if !teacher.Email && !teacher.InApp {
		return nil
	}
	summary, err := run.Summarise(ctx, teacher.ID, week)
	if err != nil || !summary.Active() {
		return err
	}
	return s.write(ctx, run, teacher, summary, done)
}

func (s SendWeeklySummaryHandler) write(ctx context.Context, run domain.ScheduledRun, teacher domain.WeeklyTeacher, summary domain.WeeklyFigures, done *Scheduled) error {
	if teacher.Email {
		wrote, err := run.Enqueue(ctx, summary.Email(teacher.ID))
		if err != nil {
			return err
		}
		if wrote {
			done.Emails++
		}
	}
	if teacher.InApp {
		wrote, err := run.Publish(ctx, summary.Notice(teacher.ID))
		if err != nil {
			return err
		}
		if wrote {
			done.InApp++
		}
	}
	return nil
}
