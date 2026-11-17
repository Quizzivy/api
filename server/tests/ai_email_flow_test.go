//go:build e2e

package e2e

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"quizzivy/internal/platform/email"
)

// aiEmails counts the AI emails of template queued for userID.
func (w *world) aiEmails(t *testing.T, userID, template string) int {
	t.Helper()
	var n int
	if err := w.pool.QueryRow(context.Background(), `SELECT count(*) FROM app.email_outbox WHERE to_user_id = $1::uuid AND template = $2`, userID, template).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// crossWarning makes the teacher's next AI call cross the centre's warning
// share: one call creates the month's row, the row is moved to one credit
// short of the share, and a second call crosses it.
func (w *world) crossWarning(t *testing.T, teacher *client, teacherID string) {
	t.Helper()
	generate := map[string]any{"standalone": true, "source": "phrasal verbs", "types": []string{"short_answer"}, "count": 1, "level": "b1", "explanations": true, "language": "en"}
	teacher.must(http.StatusOK, http.MethodPost, "/teacher/ai/questions/generate", generate)
	tag, err := w.pool.Exec(context.Background(), `
		UPDATE app.ai_teacher_credits t SET used_credits = t.allowance_credits * s.warn_percent / 100 - 1
		  FROM app.ai_settings s
		 WHERE t.user_id = $1::uuid AND t.warned_at IS NULL AND t.reserved_credits = 0`, teacherID)
	if err != nil || tag.RowsAffected() != 1 {
		t.Fatalf("moving the teacher to the warning share: %v (%d rows)", err, tag.RowsAffected())
	}
	teacher.must(http.StatusOK, http.MethodPost, "/teacher/ai/questions/generate", generate)
}

// TestEachAIKindEmailsItsRecipientOnceInTheirOwnLanguage is T-R7.23's test
// that each AI kind enqueues one email in both locales, through HTTP and
// R5's dispatcher: a teacher who crosses the warning share, each holder of
// ai.credits.manage told of a request, and the teacher told how it was
// decided each get one email, however often the AI is used after the
// warning, written in their own language and linking to Settings › AI
// assistant or Admin › AI credits on the staff host. The dispatcher runs
// before the decisions, as it does within seconds of each notice: a request
// email still waiting once the request is decided, or a warning once an
// approval lifted the teacher below the share, is dropped.
func TestEachAIKindEmailsItsRecipientOnceInTheirOwnLanguage(t *testing.T) {
	w := bootAssisted(t, newAssistant())
	adminEmail, adminPassword := w.createStaff("admin")
	admin := w.signedIn(adminEmail, adminPassword)
	adminID := admin.me(t)
	managerEmail, managerPassword := w.staffHolding("ai.credits.manage")
	manager := w.signedIn(managerEmail, managerPassword)
	managerID := manager.me(t)
	manager.must(http.StatusOK, http.MethodPatch, "/auth/me", map[string]any{"locale": "en"})

	type teacherOf struct {
		email, id string
		c         *client
		request   string
	}
	var teachers [2]teacherOf
	for i, locale := range []string{"vi", "en"} {
		email, password := w.createStaff("teacher")
		c := w.signedIn(email, password)
		c.must(http.StatusOK, http.MethodPatch, "/auth/me", map[string]any{"locale": locale})
		teachers[i] = teacherOf{email: email, id: c.me(t), c: c}
	}
	vi, en := &teachers[0], &teachers[1]

	for _, teacher := range teachers {
		w.crossWarning(t, teacher.c, teacher.id)
		teacher.c.must(http.StatusOK, http.MethodPost, "/teacher/ai/questions/generate", map[string]any{
			"standalone": true, "source": "phrasal verbs", "types": []string{"short_answer"}, "count": 1, "level": "b1", "explanations": true, "language": "en",
		})
		if n := w.aiEmails(t, teacher.id, "ai_allowance_warning"); n != 1 {
			t.Errorf("a teacher who crossed the warning share and kept going has %d warning emails, want 1", n)
		}
	}
	for _, teacher := range []*teacherOf{vi, en} {
		teacher.request = id(teacher.c.must(http.StatusCreated, http.MethodPost, "/teacher/ai/credit-requests", map[string]any{"amount": 5000, "reason": "Ba lớp thi cuối kỳ"}))
	}
	for _, m := range []string{adminID, managerID} {
		if n := w.aiEmails(t, m, "ai_credit_request"); n != 2 {
			t.Errorf("manager %s has %d request emails for the two requests, want 2", m, n)
		}
	}
	d, before := w.dispatcherAt(t, time.Now())
	drain(t, d)

	admin.must(http.StatusOK, http.MethodPost, "/admin/ai/credit-requests/"+vi.request+"/approve", nil)
	admin.must(http.StatusOK, http.MethodPost, "/admin/ai/credit-requests/"+en.request+"/decline", nil)
	for _, teacher := range teachers {
		if n := w.aiEmails(t, teacher.id, "ai_credit_request_decided"); n != 1 {
			t.Errorf("the teacher has %d decision emails, want 1", n)
		}
	}
	d, after := w.dispatcherAt(t, time.Now())
	drain(t, d)
	delivered := func(address string) []email.Message { return append(before.to(address), after.to(address)...) }
	staff := strings.TrimRight(w.cfg.Hosts.Staff, "/")
	for _, c := range []struct {
		who, address string
		want         []string
	}{
		{"the Vietnamese teacher", vi.email, []string{"tín dụng AI trong tháng", "Yêu cầu thêm 5.000 tín dụng AI của bạn đã được duyệt"}},
		{"the English teacher", en.email, []string{"AI credits for", "Your request for 5,000 more AI credits was declined"}},
		{"the Vietnamese Admin", adminEmail, []string{"xin thêm 5.000 tín dụng AI", "xin thêm 5.000 tín dụng AI"}},
		{"the English manager", managerEmail, []string{"asked for 5,000 more AI credits", "asked for 5,000 more AI credits"}},
	} {
		sent := delivered(c.address)
		if len(sent) != len(c.want) {
			t.Errorf("%s was sent %d AI emails, want %d", c.who, len(sent), len(c.want))
			continue
		}
		for i, m := range sent {
			if !strings.Contains(m.Subject, c.want[i]) {
				t.Errorf("%s's email %d is %q, want it to say %q", c.who, i+1, m.Subject, c.want[i])
			}
			link := staff + "/teacher/settings/ai\n"
			if strings.Contains(c.want[i], "xin thêm") || strings.Contains(c.want[i], "asked for") {
				link = staff + "/admin/ai\n"
			}
			if !strings.Contains(m.Text, link) || !strings.Contains(m.Text, staff+"/teacher/settings/notifications\n") {
				t.Errorf("%s's email %d does not link to %s and the settings on the staff host:\n%s", c.who, i+1, strings.TrimSpace(link), m.Text)
			}
			if strings.Contains(m.Text, "Ba lớp thi cuối kỳ") {
				t.Errorf("%s's email carries the teacher's reason:\n%s", c.who, m.Text)
			}
		}
	}
}
