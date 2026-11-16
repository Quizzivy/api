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
			Target: &domain.Target{Route: domain.RouteAssignment, AssignmentID: mailAssignment}, DedupeKey: domain.SubmittedKey(mailAssignment, mailNow)},
			domain.EmailSubmissionsDigest, domain.EventAttemptSubmitted,
			domain.EmailIDs{AssignmentID: mailAssignment, From: time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)},
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

func TestNoticesThatEmailNothingPlanNothing(t *testing.T) {
	for _, n := range []domain.Notice{
		{Kind: domain.ClassJoined, Params: domain.Joined{StudentName: "Nam", ClassName: "Lớp"}, DedupeKey: domain.JoinedKey(mailClass, mailStudent)},
		{Kind: domain.AssignmentOpened, Params: domain.Opened{Title: "Đề", ClosesAt: mailNow}, DedupeKey: domain.OpenedKey(mailAssignment)},
		{Kind: domain.AssignmentDueSoon, Params: domain.DueSoon{Title: "Đề", ClosesAt: mailNow}, DedupeKey: domain.DueSoonKey(mailAssignment, mailNow, domain.LeadDay)},
		{Kind: domain.AssignmentClosing, Params: domain.Closing{Title: "Đề", NotSubmitted: 1}, DedupeKey: domain.ClosingKey(mailAssignment, mailNow)},
		{Kind: domain.AIAllowanceWarning, Params: domain.AllowanceWarning{Used: 1, Allowance: 2}, DedupeKey: domain.AllowanceWarningKey("2026-11-01")},
		{Kind: domain.TestPublishedByCollaborator, Params: domain.PublishedByCollaborator{EditorName: "Khoa", Title: "Đề", Version: 2},
			DedupeKey: domain.CollaboratorPublishedKey(mailAssignment, 2)},
		{Kind: domain.AttemptFlagged, Params: domain.Flagged{StudentName: "Nam", Title: "Đề"}, DedupeKey: domain.FlaggedKey(mailAttempt)},
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
	} {
		if got := template.Event(); got != want {
			t.Errorf("%s is turned off by %q, want %q", template, got, want)
		}
	}
	if domain.MessageEmailDelay != 10*time.Minute || domain.MessageEmailSpacing != 30*time.Minute {
		t.Errorf("a message email waits %v and is spaced %v, want 10 and 30 minutes", domain.MessageEmailDelay, domain.MessageEmailSpacing)
	}
}
