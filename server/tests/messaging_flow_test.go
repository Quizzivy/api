//go:build e2e

package e2e

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func memberCanMessage(t *testing.T, c *client, class, student string) any {
	t.Helper()
	members := c.must(http.StatusOK, http.MethodGet, "/teacher/classes/"+class+"/members", nil)["items"].([]any)
	for _, m := range members {
		if row := m.(map[string]any); row["userId"] == student {
			return row["canMessage"]
		}
	}
	t.Fatalf("%s is not a member of %s", student, class)
	return nil
}

// TestAStaffMemberWritesToAStudentAndAClass is the staff side of messaging
// through HTTP (T-R7.10a, F2): the first post makes the thread and a retried
// one answers 200 with it, another staff member naming the student reads
// their own empty thread, a student who leaves the class can still be
// written to, a disabled one cannot while the thread stays readable, and an
// archived class's channel is read but takes no post. canMessage on the
// Students page and the roster follows the same rule, for the Admin too.
func TestAStaffMemberWritesToAStudentAndAClass(t *testing.T) {
	w := boot(t)
	teacher := w.signedIn(w.createStaff("teacher"))
	other := w.signedIn(w.createStaff("teacher"))
	admin := w.signedIn(w.createStaff("admin"))
	className := "Lớp nhắn tin " + nonce(t)
	class := teacher.class(className)
	created := teacher.must(http.StatusCreated, http.MethodPost, "/teacher/students", map[string]any{
		"email": "nhan-tin-" + nonce(t) + "@example.com", "fullName": "Học viên nhắn tin", "classIds": []string{class},
	})
	student := id(created["user"].(map[string]any))
	if created["user"].(map[string]any)["canMessage"] != true {
		t.Errorf("the created student's row: canMessage = %v, want true", created["user"].(map[string]any)["canMessage"])
	}
	if got := memberCanMessage(t, teacher, class, student); got != true {
		t.Errorf("the roster's canMessage = %v, want true", got)
	}
	if got := admin.must(http.StatusOK, http.MethodGet, "/teacher/students/"+student, nil)["canMessage"]; got != true {
		t.Errorf("the Admin's row of the teacher's student: canMessage = %v, want true: any staff member may write to any enabled student", got)
	}
	if got := memberCanMessage(t, admin, class, student); got != true {
		t.Errorf("the Admin's roster of the teacher's class: canMessage = %v, want true", got)
	}

	thread := "/teacher/messages/students/" + student
	clientID := uuid.NewString()
	first := teacher.must(http.StatusCreated, http.MethodPost, thread, map[string]any{"clientId": clientID, "body": "  Chào em, mai kiểm tra nhé. "})
	if first["body"] != "Chào em, mai kiểm tra nhé." || first["mine"] != true || first["sender"].(map[string]any)["side"] != "staff" {
		t.Errorf("the first post = %v", first)
	}
	if _, has := first["seenBy"]; has {
		t.Errorf("a direct message carries seenBy: %v", first)
	}
	again := teacher.must(http.StatusOK, http.MethodPost, thread, map[string]any{"clientId": clientID, "body": "Gửi lại"})
	if id(again) != id(first) {
		t.Errorf("the retry answered %v, want the first message", again)
	}
	for name, body := range map[string]map[string]any{
		"a blank body": {"clientId": uuid.NewString(), "body": " \n "},
		"no content":   {"clientId": uuid.NewString()},
	} {
		if status, got := teacher.call(http.MethodPost, thread, body); status != http.StatusUnprocessableEntity || errorCode(got) != "VALIDATION_FAILED" {
			t.Errorf("%s: %d %v, want 422 VALIDATION_FAILED", name, status, got)
		}
	}
	if status, got := teacher.call(http.MethodPost, thread, map[string]any{"clientId": uuid.NewString(), "body": "Tệp", "attachmentId": uuid.NewString()}); status != http.StatusNotFound {
		t.Errorf("a post naming a file nobody uploaded: %d %v, want 404", status, got)
	}
	if status, _ := teacher.call(http.MethodGet, thread+"?before="+id(first)+"&after="+id(first), nil); status != http.StatusBadRequest {
		t.Errorf("a page with before and after: %d, want 400", status)
	}
	if status, got := other.call(http.MethodGet, thread, nil); status != http.StatusOK || len(got["messages"].([]any)) != 0 || got["latestId"] != nil {
		t.Errorf("another teacher naming the student: %d %v, want 200 with their own empty thread, never the teacher's", status, got)
	} else if peer := got["student"].(map[string]any); len(peer["classes"].([]any)) != 0 || strings.Join(stringsOf(peer["otherClassNames"]), ",") != className || peer["canPost"] != true {
		t.Errorf("another teacher's header of the student: %v, want the teacher's class by name alone, and open to posts", peer)
	}
	teacher.must(http.StatusNoContent, http.MethodPost, thread+"/read", map[string]any{"lastReadMessageId": id(first)})

	teacher.must(http.StatusNoContent, http.MethodDelete, "/teacher/classes/"+class+"/members/"+student, nil)
	read := teacher.must(http.StatusOK, http.MethodGet, thread, nil)
	peer := read["student"].(map[string]any)
	if peer["former"] != true || peer["canPost"] != true || len(read["messages"].([]any)) != 1 || read["latestId"] != id(first) {
		t.Errorf("the thread after the student left: %v", read)
	}
	// The student is in no class now: otherClassNames is an empty list, never
	// null or absent.
	if names, ok := peer["otherClassNames"].([]any); !ok || len(names) != 0 {
		t.Errorf("the peer's otherClassNames = %#v, want an empty list", peer["otherClassNames"])
	}
	teacher.must(http.StatusCreated, http.MethodPost, thread, map[string]any{"clientId": uuid.NewString(), "body": "Em vẫn nhắn cô được nhé."})
	if got := teacher.must(http.StatusOK, http.MethodGet, "/teacher/students/"+student, nil)["canMessage"]; got != true {
		t.Errorf("the student who left: canMessage = %v, want true", got)
	}

	admin.must(http.StatusOK, http.MethodPost, "/admin/users/"+student+"/disable", nil)
	if status, got := teacher.call(http.MethodPost, thread, map[string]any{"clientId": uuid.NewString(), "body": "Còn đó không em?"}); status != http.StatusConflict || errorCode(got) != "MESSAGING_NOT_ALLOWED" {
		t.Errorf("a post to a disabled student: %d %v, want 409 MESSAGING_NOT_ALLOWED", status, got)
	}
	read = teacher.must(http.StatusOK, http.MethodGet, thread, nil)
	if peer := read["student"].(map[string]any); peer["disabled"] != true || peer["canPost"] != false || len(read["messages"].([]any)) != 2 {
		t.Errorf("the disabled student's thread: %v, want it readable with no post", read)
	}

	channel := "/teacher/messages/classes/" + class
	threads := teacher.must(http.StatusOK, http.MethodGet, "/teacher/messages/threads?tab=classes", nil)["threads"].([]any)
	if len(threads) != 1 || threads[0].(map[string]any)["lastMessageAt"] != nil || threads[0].(map[string]any)["thread"].(map[string]any)["classId"] != class {
		t.Errorf("the classes tab before any post: %v, want the class's channel, unposted", threads)
	}
	for _, row := range teacher.must(http.StatusOK, http.MethodGet, "/teacher/messages/threads?tab=all", nil)["threads"].([]any) {
		if names, ok := row.(map[string]any)["otherClassNames"].([]any); !ok || len(names) != 0 {
			t.Errorf("a list row's otherClassNames = %#v, want an empty list: the class row, and the student in no class: %v", row.(map[string]any)["otherClassNames"], row)
		}
	}
	posted := teacher.must(http.StatusCreated, http.MethodPost, channel, map[string]any{"clientId": uuid.NewString(), "body": "Nghỉ học thứ Sáu."})
	if seen := posted["seenBy"].(map[string]any); seen["of"] != float64(0) || seen["seen"] != float64(0) {
		t.Errorf("the announcement's seenBy = %v, want 0 of 0: the class has no active student", seen)
	}
	if status, _ := other.call(http.MethodPost, channel, map[string]any{"clientId": uuid.NewString(), "body": "x"}); status != http.StatusNotFound {
		t.Errorf("another teacher posting to the class: %d, want 404", status)
	}
	teacher.must(http.StatusOK, http.MethodPatch, "/teacher/classes/"+class, map[string]any{"archived": true})
	if status, got := teacher.call(http.MethodPost, channel, map[string]any{"clientId": uuid.NewString(), "body": "Thêm"}); status != http.StatusConflict || errorCode(got) != "CLASS_ARCHIVED" {
		t.Errorf("an announcement to the archived class: %d %v, want 409 CLASS_ARCHIVED", status, got)
	}
	archived := teacher.must(http.StatusOK, http.MethodGet, channel, nil)
	if archived["class"].(map[string]any)["archived"] != true || len(archived["messages"].([]any)) != 1 {
		t.Errorf("the archived channel = %v, want it readable", archived)
	}
	teacher.must(http.StatusNoContent, http.MethodPost, channel+"/read", map[string]any{"lastReadMessageId": id(posted)})
	if status, _ := teacher.call(http.MethodPost, channel+"/read", map[string]any{"lastReadMessageId": id(first)}); status != http.StatusUnprocessableEntity {
		t.Errorf("a channel pointer naming a direct message: %d, want 422", status)
	}
	if status, _ := teacher.call(http.MethodGet, "/teacher/messages/threads?tab=everything", nil); status != http.StatusBadRequest {
		t.Errorf("an unknown tab: %d, want 400", status)
	}
}
