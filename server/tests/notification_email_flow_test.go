//go:build e2e

package e2e

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"quizzivy/internal/core/wiring"
	"quizzivy/internal/platform/db"
	"quizzivy/internal/platform/email"
)

type captured struct {
	mu   sync.Mutex
	sent []email.Message
}

func (c *captured) Send(_ context.Context, m email.Message) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sent = append(c.sent, m)
	return "captured:" + m.IdempotencyKey, nil
}

func (c *captured) to(address string) []email.Message {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []email.Message
	for _, m := range c.sent {
		if m.To == address {
			out = append(out, m)
		}
	}
	return out
}

// dispatcherAt is the application's own email wiring (BuildMail, with the
// notification emails' Preparers) whose clock reads at and whose sender
// captures what it sends.
func (w *world) dispatcherAt(t *testing.T, at time.Time) (*email.Dispatcher, *captured) {
	t.Helper()
	dbx := db.NewContext(w.pool)
	logger := slog.New(slog.DiscardHandler)
	mail, err := wiring.BuildMail(w.cfg, dbx, wiring.OrganizationBranding(w.cfg, dbx, logger), logger)
	if err != nil {
		t.Fatal(err)
	}
	sender := &captured{}
	mail.Dispatcher.Sender, mail.Dispatcher.Now = sender, func() time.Time { return at }
	mail.Dispatcher.DailyLimit, mail.Dispatcher.MonthlyLimit, mail.Dispatcher.Interval = 10_000, 100_000, 0
	return mail.Dispatcher, sender
}

func drain(t *testing.T, d *email.Dispatcher) {
	t.Helper()
	for range 50 {
		batch, err := d.Dispatch(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if batch.Claimed == 0 && !batch.Busy {
			return
		}
	}
}

type outboxRow struct {
	id, key, params string
	deliverAfter    time.Time
	sentAt          *time.Time
	failedAt        *time.Time
	lastError       *string
}

func (w *world) messageEmails(t *testing.T, userID string) []outboxRow {
	t.Helper()
	rows, err := w.pool.Query(context.Background(), `
		SELECT id::text, dedupe_key, params::text, deliver_after, sent_at, failed_at, last_error_code
		  FROM app.email_outbox WHERE to_user_id = $1::uuid AND template = 'message_received' ORDER BY created_at, id`, userID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []outboxRow
	for rows.Next() {
		var r outboxRow
		if err := rows.Scan(&r.id, &r.key, &r.params, &r.deliverAfter, &r.sentAt, &r.failedAt, &r.lastError); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

// TestAMessageEmailWaitsForTheThreadAndNeverCarriesTheMessage is T-R7.13's
// message email through HTTP and R5's dispatcher: a student's message queues
// the teacher one email ten minutes out, with ids alone; once the teacher
// reads the thread it is closed as SKIPPED and never sent; the next message
// queues another, which goes ten minutes later to the staff host's thread,
// names the student, links to the teacher's settings, and holds no word of
// the message.
func TestAMessageEmailWaitsForTheThreadAndNeverCarriesTheMessage(t *testing.T) {
	w := boot(t)
	teacherEmail, teacherPassword := w.createStaff("teacher")
	teacher := w.signedIn(teacherEmail, teacherPassword)
	teacherID := id(teacher.must(http.StatusOK, http.MethodGet, "/auth/me", nil))
	class := teacher.class("Lớp thư " + nonce(t))
	created := teacher.must(http.StatusCreated, http.MethodPost, "/teacher/students", map[string]any{
		"email": "thu-" + nonce(t) + "@example.com", "fullName": "Học viên Thư", "classIds": []string{class},
	})
	studentID := id(created["user"].(map[string]any))
	student := w.settledIn(created["user"].(map[string]any)["email"].(string), created["temporaryPassword"].(string))

	studentThread := "/app/messages/teachers/" + teacherID
	before := time.Now()
	first := student.must(http.StatusCreated, http.MethodPost, studentThread, map[string]any{"clientId": uuid.NewString(), "body": "Cô ơi em nộp bài muộn được không ạ?"})
	queued := w.messageEmails(t, teacherID)
	if len(queued) != 1 || queued[0].deliverAfter.Before(before.Add(10*time.Minute)) || queued[0].sentAt != nil {
		t.Fatalf("the student's message queued %+v, want one email ten minutes out", queued)
	}
	if strings.Contains(queued[0].params, "muộn") || !strings.Contains(queued[0].params, studentID) {
		t.Errorf("the email's row holds %s, want the thread's ids and no word of the message", queued[0].params)
	}

	teacher.must(http.StatusNoContent, http.MethodPost, "/teacher/messages/students/"+studentID+"/read", map[string]any{"lastReadMessageId": id(first)})
	d, sender := w.dispatcherAt(t, time.Now().Add(11*time.Minute))
	drain(t, d)
	dropped := w.messageEmails(t, teacherID)[0]
	if dropped.sentAt != nil || dropped.failedAt == nil || dropped.lastError == nil || *dropped.lastError != email.SkippedCode || len(sender.to(teacherEmail)) != 0 {
		t.Fatalf("the email of a thread read since is %+v (sent %d), want closed as SKIPPED and never sent", dropped, len(sender.to(teacherEmail)))
	}

	student.must(http.StatusCreated, http.MethodPost, studentThread, map[string]any{"clientId": uuid.NewString(), "body": "Em cảm ơn cô ạ."})
	if got := w.messageEmails(t, teacherID); len(got) != 2 || got[1].key == dropped.key || got[1].sentAt != nil {
		t.Fatalf("the message after a dropped email queued %+v", got)
	}
	d, sender = w.dispatcherAt(t, time.Now().Add(11*time.Minute))
	drain(t, d)
	sent := sender.to(teacherEmail)
	if len(sent) != 1 {
		t.Fatalf("the unread thread's email was sent %d times, want once", len(sent))
	}
	m := sent[0]
	for _, part := range []string{m.Subject, m.Text, m.HTML} {
		if strings.Contains(part, "cảm ơn") || strings.Contains(part, "muộn") {
			t.Errorf("the email carries the message: %s", part)
		}
	}
	if !strings.Contains(m.Subject, "Học viên Thư") || !strings.Contains(m.Text, "/teacher/messages/student/"+studentID+"\n") ||
		!strings.Contains(m.Text, "/teacher/settings/notifications\n") {
		t.Errorf("the email does not name the student, link to the thread on the staff host and to the settings:\n%s\n%s", m.Subject, m.Text)
	}
}

// TestAStudentsSwitchTurnsOffTheirMessageEmailsAndATeachersEmailColumnTheirs
// is the preference matrix through HTTP: a student who turns Messages from
// teachers off in the app gets no email either, whatever the email half of
// their PUT said (DG-82), and a teacher who turns New message's email off
// keeps the bell and gets no email.
func TestAStudentsSwitchTurnsOffTheirMessageEmailsAndATeachersEmailColumnTheirs(t *testing.T) {
	w := boot(t)
	teacherEmail, teacherPassword := w.createStaff("teacher")
	teacher := w.signedIn(teacherEmail, teacherPassword)
	teacherID := id(teacher.must(http.StatusOK, http.MethodGet, "/auth/me", nil))
	class := teacher.class("Lớp công tắc " + nonce(t))
	created := teacher.must(http.StatusCreated, http.MethodPost, "/teacher/students", map[string]any{
		"email": "cong-tac-" + nonce(t) + "@example.com", "fullName": "Học viên Công tắc", "classIds": []string{class},
	})
	studentID := id(created["user"].(map[string]any))
	student := w.settledIn(created["user"].(map[string]any)["email"].(string), created["temporaryPassword"].(string))

	off := func(c *client, event string, inApp, mail bool) {
		t.Helper()
		items := c.must(http.StatusOK, http.MethodGet, "/me/notification-preferences", nil)["items"].([]any)
		for _, item := range items {
			if s := item.(map[string]any); s["event"] == event {
				s["inApp"], s["email"] = inApp, mail
			}
		}
		c.must(http.StatusOK, http.MethodPut, "/me/notification-preferences", items)
	}
	off(student, "message.received", false, true)
	off(teacher, "message.received", true, false)

	teacher.must(http.StatusCreated, http.MethodPost, "/teacher/messages/students/"+studentID, map[string]any{"clientId": uuid.NewString(), "body": "Chào em."})
	student.must(http.StatusCreated, http.MethodPost, "/app/messages/teachers/"+teacherID, map[string]any{"clientId": uuid.NewString(), "body": "Em chào cô."})
	if got := w.messageEmails(t, studentID); len(got) != 0 {
		t.Errorf("a student who turned Messages from teachers off is queued %+v: one switch covers both channels (DG-82)", got)
	}
	if got := w.messageEmails(t, teacherID); len(got) != 0 {
		t.Errorf("a teacher who turned the email of New message off is queued %+v", got)
	}
}
