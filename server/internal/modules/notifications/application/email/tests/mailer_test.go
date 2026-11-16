package email_test

import (
	"context"
	"errors"
	"testing"
	"time"

	notificationsemail "quizzivy/internal/modules/notifications/application/email"
	"quizzivy/internal/modules/notifications/domain"
)

const (
	reader  = "0192a7c0-0000-7000-8000-0000000000a1"
	attempt = "0192a7c0-0000-7000-8000-0000000000a2"
)

type outbox struct {
	emails []domain.Email
	err    error
}

func (o *outbox) Enqueue(_ context.Context, e domain.Email) (bool, error) {
	o.emails = append(o.emails, e)
	return true, o.err
}

func flag() domain.Notice {
	return domain.Notice{UserID: reader, Kind: domain.AttemptFlagged, Params: domain.Flagged{StudentName: "Nam", Title: "Đề"},
		Target: &domain.Target{Route: domain.RouteAttempt, AttemptID: attempt}, DedupeKey: domain.FlaggedKey(attempt)}
}

// TestTheKillSwitchKeepsAnEmailOutOfTheOutbox fails when NOTIFY_EMAIL_EVENTS
// leaves an email out and the mailer still enqueues it, when a notice that
// emails nothing reaches the outbox, or when the outbox's failure is hidden.
func TestTheKillSwitchKeepsAnEmailOutOfTheOutbox(t *testing.T) {
	now := time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)
	o := &outbox{}
	m := notificationsemail.Mailer{Outbox: o, Sends: func(event string) bool { return event != "attempt.flagged" }}
	if err := m.Mail(context.Background(), flag(), now); err != nil || len(o.emails) != 0 {
		t.Errorf("a turned-off email was enqueued: %+v %v", o.emails, err)
	}
	m.Sends = nil
	if err := m.Mail(context.Background(), flag(), now); err != nil || len(o.emails) != 1 || o.emails[0].Template != domain.EmailAttemptFlagged ||
		*o.emails[0].Switch != domain.EventAttemptFlagged || o.emails[0].UserID != reader {
		t.Errorf("with every email on, the flag was not enqueued under its switch: %+v %v", o.emails, err)
	}
	joined := domain.Notice{UserID: reader, Kind: domain.ClassJoined, Params: domain.Joined{StudentName: "Nam", ClassName: "Lớp"}, DedupeKey: "joined:x:y"}
	if err := m.Mail(context.Background(), joined, now); err != nil || len(o.emails) != 1 {
		t.Errorf("a join, which emails nothing, reached the outbox: %+v %v", o.emails, err)
	}
	o.err = errors.New("database gone")
	if err := m.Mail(context.Background(), flag(), now); !errors.Is(err, o.err) {
		t.Errorf("a failed enqueue answered %v", err)
	}
}
