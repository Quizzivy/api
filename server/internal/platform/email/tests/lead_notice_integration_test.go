//go:build integration

package email_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"quizzivy/internal/platform/email"
)

// TestALeadNoticeToAnAddressNoAccountHasOpensAtTheStaffHostInTheCentresTime
// fails when the dispatcher renders the notice of a consultation request for
// the student host (an address no account has is otherwise a student's), in a
// language the outbox did not default to, or with its received time in UTC
// because the dispatcher's zone never reached the template. The host test
// sends the export notice to an account; the template test renders without a
// dispatcher.
func TestALeadNoticeToAnAddressNoAccountHasOpensAtTheStaffHostInTheCentresTime(t *testing.T) {
	w := newWorld(t, day(22))
	to := addr(t, "tu-van")
	const leadID = "0192a7c0-0000-7000-8000-000000000003"
	w.enqueue(t, email.Mail{To: to, Params: email.LeadReceivedParams{
		LeadID: leadID, GivenName: "Mai", Interests: []string{"toeic"}, CourseFor: "self", CallTime: "morning",
		ReceivedAt: time.Date(2026, 11, 15, 18, 30, 0, 0, time.UTC),
	}})
	if _, err := w.disp.Dispatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	sent := w.sender.to(to)
	if len(sent) != 1 {
		t.Fatalf("sent %d times, want once", len(sent))
	}
	got := sent[0]
	if got.Subject != "Trung tâm Ánh Dương: Yêu cầu tư vấn mới · Mai" {
		t.Errorf("subject = %q, want the Vietnamese notice", got.Subject)
	}
	if !strings.Contains(got.Text, staffHost+"/admin/leads/"+leadID) || strings.Contains(got.Text, studentHost) {
		t.Errorf("the link is not at the staff host:\n%s", got.Text)
	}
	if !strings.Contains(got.Text, "Nhận lúc: 01:30 16/11/2026 (GMT+07:00)\n") {
		t.Errorf("the received time is not in the dispatcher's zone:\n%s", got.Text)
	}
	if got.Tags["template"] != "lead_received" {
		t.Errorf("tags = %v", got.Tags)
	}
}
