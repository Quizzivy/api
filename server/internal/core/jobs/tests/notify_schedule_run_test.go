package jobs_test

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"quizzivy/internal/core/jobs"
	notificationsapp "quizzivy/internal/modules/notifications/application"
	notificationscommand "quizzivy/internal/modules/notifications/application/command"
	"quizzivy/internal/shared/cqrs"
)

// TestOneScheduledRunIsOneCommandUnderOneLock: the API's run of the
// notification scheduler is the module's RunSchedule alone, which writes
// the due emails and the weekly summaries in one transaction under one take
// of the lock, never the two halves one after the other.
func TestOneScheduledRunIsOneCommandUnderOneLock(t *testing.T) {
	at := time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)
	var runs []notificationscommand.RunSchedule
	answer := notificationscommand.Scheduled{Emails: 2, InApp: 1}
	half := func(name string) func(context.Context) (notificationscommand.Scheduled, error) {
		return func(context.Context) (notificationscommand.Scheduled, error) {
			t.Errorf("the run called %s on its own, under a lock of its own", name)
			return notificationscommand.Scheduled{}, nil
		}
	}
	due, weekly := half("SendDueEmails"), half("SendWeeklySummary")
	app := &notificationsapp.Application{Commands: notificationsapp.Commands{
		RunSchedule: cqrs.HandlerFunc[notificationscommand.RunSchedule, notificationscommand.Scheduled](
			func(_ context.Context, cmd notificationscommand.RunSchedule) (notificationscommand.Scheduled, error) {
				runs = append(runs, cmd)
				return answer, nil
			}),
		SendDueEmails: cqrs.HandlerFunc[notificationscommand.SendDueEmails, notificationscommand.Scheduled](
			func(ctx context.Context, _ notificationscommand.SendDueEmails) (notificationscommand.Scheduled, error) {
				return due(ctx)
			}),
		SendWeeklySummary: cqrs.HandlerFunc[notificationscommand.SendWeeklySummary, notificationscommand.Scheduled](
			func(ctx context.Context, _ notificationscommand.SendWeeklySummary) (notificationscommand.Scheduled, error) {
				return weekly(ctx)
			}),
	}}
	var logs bytes.Buffer
	s := jobs.NotificationSchedule(app, time.Minute, slog.New(slog.NewJSONHandler(&logs, nil)))

	busy, err := s.Run(context.Background(), at)
	if err != nil || busy {
		t.Fatalf("run: busy %v, %v", busy, err)
	}
	if len(runs) != 1 || !runs[0].Now.Equal(at) {
		t.Errorf("RunSchedule was called %d times (%+v), want once as of the run's now", len(runs), runs)
	}
	if !strings.Contains(logs.String(), `"emails":2,"in_app":1`) {
		t.Errorf("the log is %s, want the run's totals", logs.String())
	}

	answer = notificationscommand.Scheduled{Busy: true}
	if busy, err := s.Run(context.Background(), at); err != nil || !busy {
		t.Errorf("a run another machine held: busy %v, %v", busy, err)
	}
}
