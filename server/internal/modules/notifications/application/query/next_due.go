package query

import (
	"context"
	"time"

	"quizzivy/internal/modules/notifications/application/internal/support"
	"quizzivy/internal/modules/notifications/domain"
)

// NextDue asks when the notification scheduler next has an email or a
// weekly summary to write after Now.
type NextDue struct {
	Now time.Time
}

// Due is NextDue's answer: At, or nothing due at all, in which case the
// scheduler waits domain.ScheduleIdle.
type Due struct {
	At  time.Time
	Any bool
}

type NextDueHandler struct {
	*support.Service
}

// Handle reads the store's next moments (domain.Upcoming) and keeps those of
// the emails NOTIFY_EMAIL_EVENTS leaves on; the weekly summary counts while
// its email or anyone's in-app item can be on. Without the scheduler's store
// nothing is due.
func (s NextDueHandler) Handle(ctx context.Context, q NextDue) (Due, error) {
	if s.Schedule == nil {
		return Due{}, nil
	}
	upcoming, err := s.Schedule.Upcoming(ctx, q.Now, domain.WeeklyOn{Email: s.EmailOn(domain.EmailWeeklySummary)})
	if err != nil {
		return Due{}, err
	}
	at, ok := upcoming.Next(q.Now, s.Sends)
	return Due{At: at, Any: ok}, nil
}
