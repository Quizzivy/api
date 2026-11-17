//go:build e2e

package e2e

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
)

func (c *client) unreadMessages(t *testing.T, path string) any {
	t.Helper()
	return c.must(http.StatusOK, http.MethodGet, path, nil)["unreadMessages"]
}

func (c *client) messageItems(t *testing.T, audience string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, item := range objects(c.must(http.StatusOK, http.MethodGet, "/me/notifications?audience="+audience, nil)["items"]) {
		if kind := item["kind"]; kind == "message.received" || kind == "message.from_teacher" {
			out = append(out, item)
		}
	}
	return out
}

// TestNewMessagesCountInTheShellAndRingTheBellUntilRead is T-R7.12 through
// HTTP: the summaries count other people's messages past the reader's
// pointers, each thread leaves its reader one bell item that counts its new
// messages and clears when the pointer reaches the newest, and a reader whose
// message.received switch is off still counts their messages but is told of
// none.
func TestNewMessagesCountInTheShellAndRingTheBellUntilRead(t *testing.T) {
	w := boot(t)
	teacher := w.signedIn(w.createStaff("teacher"))
	teacherID := id(teacher.must(http.StatusOK, http.MethodGet, "/auth/me", nil))
	className := "Lớp chuông " + nonce(t)
	class := teacher.class(className)
	created := teacher.must(http.StatusCreated, http.MethodPost, "/teacher/students", map[string]any{
		"email": "chuong-" + nonce(t) + "@example.com", "fullName": "Học viên chuông", "classIds": []string{class},
	})
	studentID := id(created["user"].(map[string]any))
	student := w.settledIn(created["user"].(map[string]any)["email"].(string), created["temporaryPassword"].(string))

	staffThread := "/teacher/messages/students/" + studentID
	teacher.must(http.StatusCreated, http.MethodPost, staffThread, map[string]any{"clientId": uuid.NewString(), "body": "Chào em."})
	newest := teacher.must(http.StatusCreated, http.MethodPost, staffThread, map[string]any{"clientId": uuid.NewString(), "body": "Mai lớp kiểm tra bài 3, em nhớ ôn nhé."})
	announced := teacher.must(http.StatusCreated, http.MethodPost, "/teacher/messages/classes/"+class, map[string]any{"clientId": uuid.NewString(), "body": "Thứ Sáu nghỉ học."})

	if got := student.unreadMessages(t, "/me/summary"); got != float64(3) {
		t.Errorf("the student's summary counts %v unread messages, want 3", got)
	}
	if got := teacher.unreadMessages(t, "/teacher/summary"); got != float64(0) {
		t.Errorf("the teacher's nav counts %v, want 0: only her own messages", got)
	}
	if got := teacher.unreadMessages(t, "/me/summary"); got != float64(0) {
		t.Errorf("the teacher's /me/summary counts %v, want 0: the staff count is the nav's", got)
	}
	items := student.messageItems(t, "student")
	if len(items) != 2 {
		t.Fatalf("the student's bell holds %v, want one item per thread", items)
	}
	channel, direct := items[0], items[1]
	if p := channel["params"].(map[string]any); channel["kind"] != "message.from_teacher" || p["className"] != className || p["count"] != float64(1) ||
		p["excerpt"] != "Thứ Sáu nghỉ học." || channel["target"].(map[string]any)["classId"] != class || channel["readAt"] != nil {
		t.Errorf("the channel's item, first: %v", channel)
	}
	if p := direct["params"].(map[string]any); direct["kind"] != "message.from_teacher" || p["count"] != float64(2) || p["className"] != nil ||
		p["excerpt"] != "Mai lớp kiểm tra bài 3, em nhớ ôn nhé." || direct["target"].(map[string]any)["route"] != "studentMessages" ||
		direct["target"].(map[string]any)["teacherId"] != teacherID {
		t.Errorf("the direct thread's item: %v", direct)
	}

	thread := "/app/messages/teachers/" + teacherID
	student.must(http.StatusNoContent, http.MethodPost, thread+"/read", map[string]any{"lastReadMessageId": id(newest)})
	if got := student.unreadMessages(t, "/me/summary"); got != float64(1) {
		t.Errorf("after reading the thread the student counts %v, want the announcement's 1", got)
	}
	items = student.messageItems(t, "student")
	if len(items) != 2 || items[0]["readAt"] != nil || items[1]["readAt"] == nil {
		t.Errorf("after reading the thread the bell holds %v, want its item read and the channel's not", items)
	}
	student.must(http.StatusNoContent, http.MethodPost, "/app/messages/classes/"+class+"/read", map[string]any{"lastReadMessageId": id(announced)})
	if got := student.unreadMessages(t, "/me/summary"); got != float64(0) {
		t.Errorf("after reading both the student counts %v, want 0", got)
	}

	student.must(http.StatusCreated, http.MethodPost, thread, map[string]any{"clientId": uuid.NewString(), "body": "Dạ em cảm ơn cô."})
	if got := teacher.unreadMessages(t, "/teacher/summary"); got != float64(1) {
		t.Errorf("the teacher's nav counts %v after the reply, want 1", got)
	}
	if got := student.unreadMessages(t, "/me/summary"); got != float64(0) {
		t.Errorf("the student's own reply counts %v, want 0", got)
	}
	bell := teacher.messageItems(t, "teacher")
	if len(bell) != 1 || bell[0]["kind"] != "message.received" || bell[0]["target"].(map[string]any)["studentId"] != studentID ||
		bell[0]["params"].(map[string]any)["excerpt"] != "Dạ em cảm ơn cô." {
		t.Errorf("the teacher's bell holds %v, want one message.received leading to the student's thread", bell)
	}

	switches := student.must(http.StatusOK, http.MethodGet, "/me/notification-preferences", nil)["items"].([]any)
	for _, s := range switches {
		if s := s.(map[string]any); s["event"] == "message.received" {
			s["inApp"] = false
		}
	}
	student.must(http.StatusOK, http.MethodPut, "/me/notification-preferences", switches)
	teacher.must(http.StatusCreated, http.MethodPost, staffThread, map[string]any{"clientId": uuid.NewString(), "body": "Không có gì em."})
	if got := student.unreadMessages(t, "/me/summary"); got != float64(1) {
		t.Errorf("with the switch off the student still counts %v, want 1", got)
	}
	for _, item := range student.messageItems(t, "student") {
		if item["readAt"] == nil {
			t.Errorf("with message.received off the bell holds an unread item: %v", item)
		}
	}
}
