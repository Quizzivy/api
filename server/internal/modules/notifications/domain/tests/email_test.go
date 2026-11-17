package domain_test

import (
	"strings"
	"testing"
	"time"

	"quizzivy/internal/modules/notifications/domain"
)

const (
	mailReader     = "0192a7c0-0000-7000-8000-0000000000a1"
	mailAttempt    = "0192a7c0-0000-7000-8000-0000000000a2"
	mailAssignment = "0192a7c0-0000-7000-8000-0000000000a3"
	mailThread     = "0192a7c0-0000-7000-8000-0000000000a4"
	mailStudent    = "0192a7c0-0000-7000-8000-0000000000a5"
	mailClass      = "0192a7c0-0000-7000-8000-0000000000a6"
	mailRequest    = "0192a7c0-0000-7000-8000-0000000000a7"
)

var mailNow = time.Date(2026, 11, 17, 9, 7, 30, 0, time.UTC)

// TestEachNoticeThatEmailsPlansItsTemplateIdsTimeAndSwitch fails when a
// notice that emails stops planning its email, plans one about anything but
// its ids, under another switch, with a key two recipients could share, or
// due at the wrong time.
func TestEachNoticeThatEmailsPlansItsTemplateIdsTimeAndSwitch(t *testing.T) {
	previous := "Trần Minh Khoa"
	for _, c := range []struct {
		notice   domain.Notice
		template domain.EmailTemplate
		sw       domain.Event
		ids      domain.EmailIDs
		due      time.Time
	}{
		{domain.Notice{Kind: domain.AttemptFlagged, Params: domain.Flagged{StudentName: "Nam", Title: "Đề"},
			Target: &domain.Target{Route: domain.RouteAttempt, AttemptID: mailAttempt, AssignmentID: mailAssignment}, DedupeKey: domain.FlaggedKey(mailAttempt)},
			domain.EmailAttemptFlagged, domain.EventAttemptFlagged, domain.EmailIDs{AttemptID: mailAttempt}, time.Time{}},
		{domain.Notice{Kind: domain.AssignmentExtended, Params: domain.Extended{Title: "Đề", ClosesAt: mailNow.Add(48 * time.Hour)},
			Target: &domain.Target{Route: domain.RouteStudentAssignment, AssignmentID: mailAssignment}, DedupeKey: domain.ExtendedKey(mailAssignment)},
			domain.EmailAssignmentExtended, domain.EventAssignmentDueSoon, domain.EmailIDs{AssignmentID: mailAssignment}, time.Time{}},
		{domain.Notice{Kind: domain.ResultReady, Params: domain.Ready{Title: "Đề"},
			Target: &domain.Target{Route: domain.RouteResult, AttemptID: mailAttempt, AssignmentID: mailAssignment}, DedupeKey: domain.ReadyKey(mailAttempt)},
			domain.EmailResultReady, domain.EventResultReady, domain.EmailIDs{AttemptID: mailAttempt}, time.Time{}},
		{domain.Notice{Kind: domain.AttemptSubmitted, Params: domain.Submitted{Title: "Đề", Count: 1},
			Target: &domain.Target{Route: domain.RouteAssignment, AssignmentID: mailAssignment}, DedupeKey: domain.SubmittedKey(mailAssignment, mailNow), AttemptID: mailAttempt},
			domain.EmailSubmissionsDigest, domain.EventAttemptSubmitted,
			domain.EmailIDs{AssignmentID: mailAssignment, AttemptID: mailAttempt, From: time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)},
			time.Date(2026, 11, 17, 9, 15, 0, 0, time.UTC)},
		{domain.Notice{Kind: domain.MessageReceived, Params: domain.MessageNotice{SenderName: "My", Count: 1, Excerpt: "Chào cô"},
			Target: &domain.Target{Route: domain.RouteMessages, StudentID: mailStudent}, DedupeKey: domain.MessageKey(mailThread)},
			domain.EmailMessageReceived, domain.EventMessageReceived, domain.EmailIDs{ConversationID: mailThread, StudentID: mailStudent}, mailNow.Add(10 * time.Minute)},
		{domain.Notice{Kind: domain.MessageFromTeacher, Params: domain.TeacherMessageNotice{SenderName: "Cô Thương", Count: 1, Excerpt: "Nghỉ học"},
			Target: &domain.Target{Route: domain.RouteStudentMessages, ClassID: mailClass}, DedupeKey: domain.MessageKey(mailThread)},
			domain.EmailMessageReceived, domain.EventMessageReceived, domain.EmailIDs{ConversationID: mailThread, ClassID: mailClass}, mailNow.Add(10 * time.Minute)},
		{domain.Notice{Kind: domain.ContentShared, Params: domain.Shared{SharerName: "Lan", ContentKind: domain.ShareQuestionSet, Name: "Bộ", Access: domain.ShareUse},
			Target: &domain.Target{Route: domain.RouteSharedWithMe}, DedupeKey: domain.SharedKey(domain.ShareQuestionSet, mailAssignment)},
			domain.EmailContentShared, "", domain.EmailIDs{ContentKind: domain.ShareQuestionSet, ResourceID: mailAssignment}, time.Time{}},
		{domain.Notice{Kind: domain.ClassTeacherChanged, Params: domain.TeacherChanged{ClassName: "Lớp", TeacherName: &previous},
			Target: &domain.Target{Route: domain.RouteClasses}, DedupeKey: domain.TeacherChangedKey(mailClass)},
			domain.EmailClassTeacherChanged, "", domain.EmailIDs{ClassID: mailClass, Previous: true}, time.Time{}},
		{domain.Notice{Kind: domain.AIAllowanceWarning, Params: domain.AllowanceWarning{Used: 16_500, Allowance: 20_000},
			DedupeKey: domain.AllowanceWarningKey("2026-11-01"), Merge: domain.Replace},
			domain.EmailAIAllowanceWarning, "", domain.EmailIDs{Month: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)}, time.Time{}},
		{domain.Notice{Kind: domain.AICreditRequest, Params: domain.CreditRequest{TeacherName: "Lan", Credits: 5_000},
			DedupeKey: domain.CreditRequestKey(mailRequest), Merge: domain.Replace},
			domain.EmailAICreditRequest, "", domain.EmailIDs{RequestID: mailRequest}, time.Time{}},
		{domain.Notice{Kind: domain.AICreditRequestDecided, Params: domain.CreditRequestDecided{Credits: 5_000, Decision: domain.DecisionDeclined},
			DedupeKey: domain.CreditDecisionKey(mailRequest), Merge: domain.Replace},
			domain.EmailAICreditRequestDecided, "", domain.EmailIDs{RequestID: mailRequest}, time.Time{}},
	} {
		c.notice.UserID = mailReader
		e, ok := domain.PlanEmail(c.notice, mailNow)
		if !ok {
			t.Errorf("%s plans no email", c.notice.Kind)
			continue
		}
		if e.Template != c.template || e.UserID != mailReader || !e.DeliverAfter.Equal(c.due) || e.IDs != c.ids {
			t.Errorf("%s plans %s for %s due %v about %+v, want %s for the reader due %v about %+v",
				c.notice.Kind, e.Template, e.UserID, e.DeliverAfter, e.IDs, c.template, c.due, c.ids)
		}
		if (c.sw == "") != (e.Switch == nil) || e.Switch != nil && *e.Switch != c.sw {
			t.Errorf("%s is governed by %v, want %q", c.notice.Kind, e.Switch, c.sw)
		}
		if !strings.HasPrefix(e.DedupeKey, "email:") || len(e.DedupeKey) > 200 || (c.template != domain.EmailResultReady && !strings.Contains(e.DedupeKey, mailReader)) {
			t.Errorf("%s's key %q is not the reader's own", c.notice.Kind, e.DedupeKey)
		}
		if (c.template == domain.EmailMessageReceived) != (e.Thread == mailThread) {
			t.Errorf("%s's thread is %q", c.notice.Kind, e.Thread)
		}
	}
}

// TestADigestTakesTheBucketOfItsInAppItem fails when a paper's digest is
// bucketed by the moment Notify runs rather than by the notice's own key,
// which names the moment the paper was announced: the bell and the email
// would then count it in different buckets.
func TestADigestTakesTheBucketOfItsInAppItem(t *testing.T) {
	announced := time.Date(2026, 11, 17, 9, 14, 59, 0, time.UTC)
	n := domain.Notice{UserID: mailReader, Kind: domain.AttemptSubmitted, Params: domain.Submitted{Title: "Đề", Count: 1},
		Target:    &domain.Target{Route: domain.RouteAssignment, AssignmentID: mailAssignment},
		DedupeKey: domain.SubmittedKey(mailAssignment, announced), AttemptID: mailAttempt}
	e, ok := domain.PlanEmail(n, announced.Add(2*time.Second))
	from := time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)
	if !ok || !e.IDs.From.Equal(from) || !e.DeliverAfter.Equal(from.Add(domain.SubmittedBucket)) {
		t.Errorf("a paper announced at %v and mailed in the next bucket plans %+v, want the bucket from %v", announced, e, from)
	}
	if got, ok := domain.SubmittedFrom(domain.SubmittedKey(mailAssignment, announced)); !ok || !got.Equal(from) {
		t.Errorf("the key's bucket reads %v %v, want %v", got, ok, from)
	}
	for _, key := range []string{"submitted:", "submitted:" + mailAssignment, "submitted:" + mailAssignment + ":x", domain.FlaggedKey(mailAttempt)} {
		if _, ok := domain.SubmittedFrom(key); ok {
			t.Errorf("%q reads as a submission's bucket", key)
		}
	}
}

func TestNoticesThatEmailNothingPlanNothing(t *testing.T) {
	for _, n := range []domain.Notice{
		{Kind: domain.ClassJoined, Params: domain.Joined{StudentName: "Nam", ClassName: "Lớp"}, DedupeKey: domain.JoinedKey(mailClass, mailStudent)},
		{Kind: domain.AssignmentOpened, Params: domain.Opened{Title: "Đề", ClosesAt: mailNow}, DedupeKey: domain.OpenedKey(mailAssignment)},
		{Kind: domain.AssignmentDueSoon, Params: domain.DueSoon{Title: "Đề", ClosesAt: mailNow}, DedupeKey: domain.DueSoonKey(mailAssignment, mailNow, domain.LeadDay)},
		{Kind: domain.AssignmentClosing, Params: domain.Closing{Title: "Đề", NotSubmitted: 1}, DedupeKey: domain.ClosingKey(mailAssignment, mailNow)},
		{Kind: domain.AIAllowanceWarning, Params: domain.AllowanceWarning{Used: 1, Allowance: 2}, DedupeKey: domain.AllowanceWarningKey("2026-11-17")},
		{Kind: domain.AIAllowanceWarning, Params: domain.AllowanceWarning{Used: 1, Allowance: 2}, DedupeKey: "ai_warning:"},
		{Kind: domain.AICreditRequest, Params: domain.CreditRequest{TeacherName: "Lan", Credits: 5_000}, DedupeKey: "ai_request:"},
		{Kind: domain.AICreditRequestDecided, Params: domain.CreditRequestDecided{Credits: 5_000, Decision: domain.DecisionApproved}, DedupeKey: domain.CreditRequestKey(mailRequest)},
		{Kind: domain.TestPublishedByCollaborator, Params: domain.PublishedByCollaborator{EditorName: "Khoa", Title: "Đề", Version: 2},
			DedupeKey: domain.CollaboratorPublishedKey(mailAssignment, 2)},
		{Kind: domain.AttemptFlagged, Params: domain.Flagged{StudentName: "Nam", Title: "Đề"}, DedupeKey: domain.FlaggedKey(mailAttempt)},
		{Kind: domain.AttemptSubmitted, Params: domain.Submitted{Title: "Đề", Count: 1}, DedupeKey: domain.SubmittedKey(mailAssignment, mailNow),
			Target: &domain.Target{Route: domain.RouteAssignment, AssignmentID: mailAssignment}},
		{Kind: domain.MessageReceived, Params: domain.MessageNotice{SenderName: "My", Count: 1, Excerpt: "Chào"}, DedupeKey: "msg:",
			Target: &domain.Target{Route: domain.RouteMessages, StudentID: mailStudent}},
		{Kind: domain.ContentShared, Params: domain.Shared{SharerName: "Lan", ContentKind: domain.ShareTest, Name: "Đề", Access: domain.ShareUse}, DedupeKey: "shared:test"},
	} {
		n.UserID = mailReader
		if e, ok := domain.PlanEmail(n, mailNow); ok {
			t.Errorf("%s plans %+v, want no email", n.Kind, e)
		}
	}
}

func TestEachEmailHasItsKillSwitchName(t *testing.T) {
	for template, want := range map[domain.EmailTemplate]string{
		domain.EmailAttemptFlagged: "attempt.flagged", domain.EmailSubmissionsDigest: "attempt.submitted",
		domain.EmailMessageReceived: "message.received", domain.EmailWeeklySummary: "summary.weekly",
		domain.EmailAIAllowanceWarning: "ai.allowance_warning", domain.EmailAICreditRequest: "ai.credit_request",
		domain.EmailAICreditRequestDecided: "ai.credit_request.decided",
	} {
		if got := template.Event(); got != want {
			t.Errorf("%s is turned off by %q, want %q", template, got, want)
		}
	}
	if domain.MessageEmailDelay != 10*time.Minute || domain.MessageEmailSpacing != 30*time.Minute {
		t.Errorf("a message email waits %v and is spaced %v, want 10 and 30 minutes", domain.MessageEmailDelay, domain.MessageEmailSpacing)
	}
}

// TestTheAIEmailsGoOnceAMonthOncePerManagerAndOncePerDecision fails when an
// AI notice's email is keyed so that a second warning in the month, or two
// managers told of one request, share a row, or one request's decision is
// keyed like another's.
func TestTheAIEmailsGoOnceAMonthOncePerManagerAndOncePerDecision(t *testing.T) {
	other := "0192a7c0-0000-7000-8000-0000000000b1"
	plan := func(n domain.Notice) string {
		t.Helper()
		e, ok := domain.PlanEmail(n, mailNow)
		if !ok {
			t.Fatalf("%s plans no email", n.Kind)
		}
		return e.DedupeKey
	}
	warning := func(user, month string) domain.Notice {
		return domain.Notice{UserID: user, Kind: domain.AIAllowanceWarning, Params: domain.AllowanceWarning{Used: 1, Allowance: 2}, DedupeKey: domain.AllowanceWarningKey(month)}
	}
	if a, b := plan(warning(mailReader, "2026-11-01")), plan(warning(mailReader, "2026-11-01")); a != b {
		t.Errorf("one teacher's two warnings in a month are keyed %q and %q, want one row", a, b)
	}
	if a, b := plan(warning(mailReader, "2026-11-01")), plan(warning(mailReader, "2026-12-01")); a == b {
		t.Errorf("two months' warnings share the key %q", a)
	}
	if a, b := plan(warning(mailReader, "2026-11-01")), plan(warning(other, "2026-11-01")); a == b {
		t.Errorf("two teachers' warnings share the key %q", a)
	}
	request := func(user string) domain.Notice {
		return domain.Notice{UserID: user, Kind: domain.AICreditRequest, Params: domain.CreditRequest{TeacherName: "Lan", Credits: 5_000}, DedupeKey: domain.CreditRequestKey(mailRequest)}
	}
	if a, b := plan(request(mailReader)), plan(request(other)); a == b {
		t.Errorf("two managers told of one request share the key %q", a)
	}
	decided := func(id string) domain.Notice {
		return domain.Notice{UserID: mailReader, Kind: domain.AICreditRequestDecided, Params: domain.CreditRequestDecided{Credits: 5_000, Decision: domain.DecisionApproved}, DedupeKey: domain.CreditDecisionKey(id)}
	}
	if a, b := plan(decided(mailRequest)), plan(decided(other)); a == b {
		t.Errorf("two requests' decisions share the key %q", a)
	}
	if got, ok := domain.AllowanceWarningOf(domain.AllowanceWarningKey("2026-11-01")); !ok || !got.Equal(time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("the warning's key reads %v %v", got, ok)
	}
	for _, key := range []string{"ai_warning:2026-11-17", "ai_warning:x", domain.CreditRequestKey(mailRequest)} {
		if _, ok := domain.AllowanceWarningOf(key); ok {
			t.Errorf("%q reads as a warning's month", key)
		}
	}
	if id, ok := domain.CreditRequestOf(domain.CreditRequestKey(mailRequest)); !ok || id != mailRequest {
		t.Errorf("the request's key reads %q %v", id, ok)
	}
	if id, ok := domain.CreditDecisionOf(domain.CreditDecisionKey(mailRequest)); !ok || id != mailRequest {
		t.Errorf("the decision's key reads %q %v", id, ok)
	}
}
