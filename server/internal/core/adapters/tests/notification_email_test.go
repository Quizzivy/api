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

func (f *facts) AllowanceWarning(_ context.Context, _ string, p email.AIAllowanceWarningParams, _ time.Time) (email.AIAllowanceWarningParams, bool, error) {
	p.Used, p.Allowance = 16_500, 20_000
	return p, f.found, nil
}

func (f *facts) CreditRequest(_ context.Context, _ string, p email.AICreditRequestParams, _ time.Time) (email.AICreditRequestParams, bool, error) {
	p.TeacherName, p.Credits = "Nguyễn Thị Lan", 5_000
	return p, f.found, nil
}

func (f *facts) CreditDecision(_ context.Context, _ string, p email.AICreditRequestDecidedParams, _ time.Time) (email.AICreditRequestDecidedParams, bool, error) {
	p.Credits, p.Decision = 5_000, email.CreditsApproved
	return p, f.found, nil
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
		string(notificationsdomain.EmailWeeklySummary), string(notificationsdomain.EmailAIAllowanceWarning),
		string(notificationsdomain.EmailAICreditRequest), string(notificationsdomain.EmailAICreditRequestDecided),
	}
	if !slices.Equal(prepared, module) {
		t.Errorf("the platform's notification emails are %v, the module's %v", prepared, module)
	}
	if email.DigestBucket != notificationsdomain.SubmittedBucket {
		t.Errorf("the digest's bucket is %v, the in-app item's %v", email.DigestBucket, notificationsdomain.SubmittedBucket)
	}
}

// TestTheAIEmailsAskNoSwitchAndDropWhatIsNoLongerTrue fails when one of the
// AI assistant's emails (T-R7.23) is held to a switch, which none of them
// has, is sent once its read no longer finds it true or NOTIFY_EMAIL_EVENTS
// names it off, or is sent without what its read filled in.
func TestTheAIEmailsAskNoSwitchAndDropWhatIsNoLongerTrue(t *testing.T) {
	const reader, request = "0192a7c0-0000-7000-8000-0000000000a1", "0192a7c0-0000-7000-8000-0000000000a3"
	november := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		params email.Params
		filled func(email.Params) bool
	}{
		{email.AIAllowanceWarningParams{Month: november}, func(p email.Params) bool {
			w := p.(email.AIAllowanceWarningParams)
			return w.Used == 16_500 && w.Allowance == 20_000 && w.Month.Equal(november)
		}},
		{email.AICreditRequestParams{RequestID: request}, func(p email.Params) bool {
			r := p.(email.AICreditRequestParams)
			return r.TeacherName == "Nguyễn Thị Lan" && r.Credits == 5_000 && r.RequestID == request
		}},
		{email.AICreditRequestDecidedParams{RequestID: request}, func(p email.Params) bool {
			d := p.(email.AICreditRequestDecidedParams)
			return d.Decision == email.CreditsApproved && d.Credits == 5_000 && d.RequestID == request
		}},
	} {
		tmpl := c.params.Template()
		draft := email.Draft{ToUserID: reader, Params: c.params}
		f := &facts{on: true, found: true}
		p, send, err := adapters.NotificationPreparers(f, nil)[tmpl].Prepare(context.Background(), draft)
		if err != nil || !send || p.Template() != tmpl || !c.filled(p) {
			t.Errorf("%s prepares as %+v (send %v, err %v)", tmpl, p, send, err)
			continue
		}
		if f.asked != nil {
			t.Errorf("%s asked about the switch %v, want none", tmpl, *f.asked)
		}
		if _, send, _ := adapters.NotificationPreparers(&facts{on: true, found: false}, nil)[tmpl].Prepare(context.Background(), draft); send {
			t.Errorf("%s is sent once its read finds it no longer true", tmpl)
		}
		off := func(event string) bool { return event != email.Event(tmpl) }
		if _, send, _ := adapters.NotificationPreparers(&facts{on: true, found: true}, off)[tmpl].Prepare(context.Background(), draft); send {
			t.Errorf("%s is sent while NOTIFY_EMAIL_EVENTS leaves %s out", tmpl, email.Event(tmpl))
		}
	}
}
