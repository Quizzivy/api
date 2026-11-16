package adapters_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"quizzivy/internal/core/adapters"
	notificationsdomain "quizzivy/internal/modules/notifications/domain"
	"quizzivy/internal/platform/email"
)

type facts struct {
	adapters.NotificationFacts
	on, found bool
	err       error
	asked     *notificationsdomain.Event
}

func (f *facts) Recipient(_ context.Context, _ string, sw *notificationsdomain.Event) (string, bool, error) {
	f.asked = sw
	return "Cô Lan", f.on, f.err
}

func (f *facts) Flagged(_ context.Context, _ string, p email.AttemptFlaggedParams, _ time.Time) (email.AttemptFlaggedParams, bool, error) {
	p.Title, p.StudentName = "Đề", "Nam"
	return p, f.found, nil
}

// TestANotificationEmailIsSentOnlyWhileItIsOnAndStillTrue fails when a
// Preparer sends an email NOTIFY_EMAIL_EVENTS turned off, one whose switch
// the recipient turned off since, or one its read no longer finds true, or
// hides a failed read, which the dispatcher must retry.
func TestANotificationEmailIsSentOnlyWhileItIsOnAndStillTrue(t *testing.T) {
	draft := email.Draft{ToUserID: "0192a7c0-0000-7000-8000-0000000000a1", Params: email.AttemptFlaggedParams{AttemptID: "0192a7c0-0000-7000-8000-0000000000a2"}}
	for name, c := range map[string]struct {
		f     *facts
		sends func(string) bool
		send  bool
		err   bool
	}{
		"on and true":                      {&facts{on: true, found: true}, nil, true, false},
		"turned off by NOTIFY_EMAIL_EVENT": {&facts{on: true, found: true}, func(string) bool { return false }, false, false},
		"its switch is off":                {&facts{on: false, found: true}, nil, false, false},
		"no longer true":                   {&facts{on: true, found: false}, nil, false, false},
		"the read failed":                  {&facts{err: errors.New("database gone")}, nil, false, true},
	} {
		p, send, err := adapters.NotificationPreparers(c.f, c.sends)[email.AttemptFlagged].Prepare(context.Background(), draft)
		if send != c.send || (err != nil) != c.err {
			t.Errorf("%s: send %v, err %v", name, send, err)
		}
		if send {
			if got := p.(email.AttemptFlaggedParams); got.FullName != "Cô Lan" || got.Title != "Đề" || got.AttemptID != "0192a7c0-0000-7000-8000-0000000000a2" {
				t.Errorf("%s: the prepared params are %+v", name, got)
			}
			if c.f.asked == nil || *c.f.asked != notificationsdomain.EventAttemptFlagged {
				t.Errorf("%s: the switch asked about is %v", name, c.f.asked)
			}
		}
	}
}

// TestEveryNotificationEmailHasAPreparerAndTheModulesName fails when a
// Prepared template has no Preparer, so that no dispatcher would ever send
// it, or when the notifications module's list of emails, kill-switch names
// or bucket drift from the email platform's.
func TestEveryNotificationEmailHasAPreparerAndTheModulesName(t *testing.T) {
	preparers := adapters.NotificationPreparers(&facts{}, nil)
	var prepared []string
	for _, tmpl := range email.Templates() {
		if !email.Prepared(tmpl) {
			continue
		}
		prepared = append(prepared, string(tmpl))
		if preparers[tmpl] == nil {
			t.Errorf("%s has no Preparer", tmpl)
		}
		if got := notificationsdomain.EmailTemplate(tmpl).Event(); got != email.Event(tmpl) {
			t.Errorf("%s is turned off by %q in the module and %q in the platform", tmpl, got, email.Event(tmpl))
		}
	}
	module := []string{
		string(notificationsdomain.EmailAttemptFlagged), string(notificationsdomain.EmailAssignmentExtended), string(notificationsdomain.EmailAssignmentDueSoon),
		string(notificationsdomain.EmailAssignmentClosing), string(notificationsdomain.EmailResultReady), string(notificationsdomain.EmailContentShared),
		string(notificationsdomain.EmailClassTeacherChanged), string(notificationsdomain.EmailSubmissionsDigest), string(notificationsdomain.EmailMessageReceived),
		string(notificationsdomain.EmailWeeklySummary),
	}
	if !slices.Equal(prepared, module) {
		t.Errorf("the platform's notification emails are %v, the module's %v", prepared, module)
	}
	if email.DigestBucket != notificationsdomain.SubmittedBucket {
		t.Errorf("the digest's bucket is %v, the in-app item's %v", email.DigestBucket, notificationsdomain.SubmittedBucket)
	}
}
