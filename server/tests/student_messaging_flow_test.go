//go:build e2e

package e2e

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// flaggedAttempt has student sit the version on class with an assignment
// that flags a second focus loss, send two absences long enough to count, and
// hand in: the attempt is flagged on an assignment teacher reaches.
func flaggedAttempt(t *testing.T, teacher, student *client, version, class string) (assignment, attempt string) {
	t.Helper()
	now := time.Now()
	assignment = id(teacher.must(http.StatusCreated, http.MethodPost, "/teacher/assignments", map[string]any{
		"testVersionId":   version,
		"targets":         map[string]any{"classIds": []string{class}, "studentIds": []string{}},
		"window":          map[string]any{"opensAt": rfc3339(now.Add(-time.Minute)), "closesAt": rfc3339(now.Add(2 * time.Hour))},
		"durationMinutes": 30, "maxAttempts": 1,
		"review": map[string]any{"showScore": true, "showCorrectAnswers": false, "showExplanations": false},
		"integrity": map[string]any{
			"requireFullscreen": false, "blockCopyPaste": true, "maxFocusLoss": 1, "onLimitExceeded": "flag", "minAwayMs": 3000,
		},
	}))
	session := student.must(http.StatusOK, http.MethodPost, "/app/assignments/"+assignment+"/attempts", nil)
	attempt = id(session["attempt"].(map[string]any))
	away := func(seq int) map[string]any {
		return map[string]any{"kind": "window_focus", "occurredAt": rfc3339(time.Now()), "clientSeq": seq, "meta": map[string]any{"awayMs": 4000}}
	}
	student.must(http.StatusAccepted, http.MethodPost, "/app/attempts/"+attempt+"/events", map[string]any{
		"sessionId": session["sessionId"], "events": []any{away(1), away(2)},
	})
	student.must(http.StatusOK, http.MethodPost, "/app/attempts/"+attempt+"/submit", map[string]any{"sessionId": session["sessionId"], "reason": "manual"})
	return assignment, attempt
}

func hasKey(v any, key string) bool {
	switch v := v.(type) {
	case map[string]any:
		for k, child := range v {
			if k == key || hasKey(child, key) {
				return true
			}
		}
	case []any:
		for _, child := range v {
			if hasKey(child, key) {
				return true
			}
		}
	}
	return false
}

// TestAStudentWritesToTheirTeacherAndReadsTheirClass is the student side of
// messaging through HTTP (T-R7.10b): the class card names the teacher whose
// thread it opens, a student writes first to the teacher of a class they are
// in and keeps writing after leaving it, reads their class's channel and
// cannot post there, never reads a staff key, and the teacher's thread shows
// the student's flagged attempt beside the messages, never as unread.
func TestAStudentWritesToTheirTeacherAndReadsTheirClass(t *testing.T) {
	w := boot(t)
	teacher := w.signedIn(w.createStaff("teacher"))
	admin := w.signedIn(w.createStaff("admin"))
	teacherID := id(teacher.must(http.StatusOK, http.MethodGet, "/auth/me", nil))
	className := "Lớp hỏi bài " + nonce(t)
	class := teacher.class(className)
	created := teacher.must(http.StatusCreated, http.MethodPost, "/teacher/students", map[string]any{
		"email": "hoi-bai-" + nonce(t) + "@example.com", "fullName": "Học viên hỏi bài", "classIds": []string{class},
	})
	studentID := id(created["user"].(map[string]any))
	student := w.settledIn(created["user"].(map[string]any)["email"].(string), created["temporaryPassword"].(string))

	cards := student.must(http.StatusOK, http.MethodGet, "/app/classes", nil)["items"].([]any)
	if len(cards) != 1 || cards[0].(map[string]any)["teacherId"] != teacherID {
		t.Fatalf("the student's class cards: %v, want the class naming its teacher, whose thread the card opens", cards)
	}
	if threads := student.must(http.StatusOK, http.MethodGet, "/app/messages/threads", nil)["threads"].([]any); len(threads) != 0 {
		t.Errorf("the list before any post: %v, want none: no channel without a post", threads)
	}

	thread := "/app/messages/teachers/" + teacherID
	empty := student.must(http.StatusOK, http.MethodGet, thread, nil)
	if head := empty["teacher"].(map[string]any); head["canPost"] != true || head["former"] != false || len(empty["messages"].([]any)) != 0 ||
		strings.Join(stringsOf(head["classNames"]), ",") != className {
		t.Errorf("the thread before the first post: %v", empty)
	}
	clientID := uuid.NewString()
	first := student.must(http.StatusCreated, http.MethodPost, thread, map[string]any{"clientId": clientID, "body": " Em chào cô, em hỏi bài 3 ạ. "})
	if first["body"] != "Em chào cô, em hỏi bài 3 ạ." || first["mine"] != true || first["sender"].(map[string]any)["id"] != studentID {
		t.Errorf("the student's first post: %v", first)
	}
	if again := student.must(http.StatusOK, http.MethodPost, thread, map[string]any{"clientId": clientID, "body": "Gửi lại"}); id(again) != id(first) {
		t.Errorf("the retry answered %v, want the first message", again)
	}
	if status, got := student.call(http.MethodPost, thread, map[string]any{"clientId": uuid.NewString(), "body": " \n "}); status != http.StatusUnprocessableEntity || errorCode(got) != "VALIDATION_FAILED" {
		t.Errorf("a blank post: %d %v, want 422 VALIDATION_FAILED", status, got)
	}
	if status, _ := student.call(http.MethodPost, thread, map[string]any{"clientId": uuid.NewString(), "body": "Tệp", "attachmentId": uuid.NewString()}); status != http.StatusBadRequest {
		t.Errorf("a student's post naming a file: %d, want 400: students send text only", status)
	}

	staffThread := "/teacher/messages/students/" + studentID
	staff := teacher.must(http.StatusOK, http.MethodGet, staffThread, nil)
	if messages := staff["messages"].([]any); len(messages) != 1 || messages[0].(map[string]any)["sender"].(map[string]any)["side"] != "student" {
		t.Errorf("the teacher reads %v, want the student's message on the student side", staff["messages"])
	}
	if events := staff["contextEvents"].([]any); len(events) != 0 {
		t.Errorf("context events before any flagged attempt: %v", events)
	}
	reply := teacher.must(http.StatusCreated, http.MethodPost, staffThread, map[string]any{"clientId": uuid.NewString(), "body": "Cô xem rồi, em làm lại câu b nhé."})
	announcement := teacher.must(http.StatusCreated, http.MethodPost, "/teacher/messages/classes/"+class, map[string]any{"clientId": uuid.NewString(), "body": "Thứ Sáu lớp kiểm tra."})

	listed := student.must(http.StatusOK, http.MethodGet, "/app/messages/threads", nil)
	kinds := map[string]map[string]any{}
	for _, row := range objects(listed["threads"]) {
		kinds[row["thread"].(map[string]any)["kind"].(string)] = row
	}
	if direct := kinds["direct"]; direct == nil || direct["thread"].(map[string]any)["teacherId"] != teacherID || direct["unread"] != float64(1) || direct["former"] != false {
		t.Errorf("the student's direct row: %v", direct)
	}
	if channel := kinds["class"]; channel == nil || channel["thread"].(map[string]any)["classId"] != class || channel["title"] != className || channel["unread"] != float64(1) {
		t.Errorf("the student's class row: %v", channel)
	}
	read := student.must(http.StatusOK, http.MethodGet, "/app/messages/classes/"+class, nil)
	if read["readOnly"] != true || read["teacher"].(map[string]any)["id"] != teacherID || len(read["messages"].([]any)) != 1 {
		t.Errorf("the student's channel: %v", read)
	}
	for label, body := range map[string]map[string]any{"the list": listed, "the channel": read, "the thread": student.must(http.StatusOK, http.MethodGet, thread, nil), "a post": first} {
		for _, key := range []string{"seenBy", "side", "contextEvents", "studentCount", "email"} {
			if hasKey(body, key) {
				t.Errorf("%s answers the student %q: %v", label, key, body)
			}
		}
	}

	post := map[string]any{"clientId": uuid.NewString(), "body": "Em chào cả lớp"}
	if status, _ := student.call(http.MethodPost, "/app/messages/classes/"+class, post); status != http.StatusMethodNotAllowed {
		t.Errorf("a student's post to their class's channel: %d, want 405: the path takes no post", status)
	}
	if status, got := student.call(http.MethodPost, "/teacher/messages/classes/"+class, post); status != http.StatusForbidden {
		t.Errorf("a student's announcement: %d %v, want 403", status, got)
	}
	if again := student.must(http.StatusOK, http.MethodGet, "/app/messages/classes/"+class, nil); len(again["messages"].([]any)) != 1 {
		t.Errorf("the channel after the student's refused posts: %v", again["messages"])
	}

	student.must(http.StatusNoContent, http.MethodPost, thread+"/read", map[string]any{"lastReadMessageId": id(reply)})
	student.must(http.StatusNoContent, http.MethodPost, "/app/messages/classes/"+class+"/read", map[string]any{"lastReadMessageId": id(announcement)})
	if status, got := student.call(http.MethodPost, thread+"/read", map[string]any{"lastReadMessageId": id(announcement)}); status != http.StatusUnprocessableEntity || errorCode(got) != "VALIDATION_FAILED" {
		t.Errorf("a direct pointer naming the announcement: %d %v, want 422", status, got)
	}
	for _, row := range objects(student.must(http.StatusOK, http.MethodGet, "/app/messages/threads", nil)["threads"]) {
		if row["unread"] != float64(0) {
			t.Errorf("after reading both: %v", row)
		}
	}
	if seen := teacher.must(http.StatusOK, http.MethodGet, "/teacher/messages/classes/"+class, nil)["messages"].([]any)[0].(map[string]any)["seenBy"].(map[string]any); seen["seen"] != float64(1) || seen["of"] != float64(1) {
		t.Errorf("the teacher's Seen by after the student read: %v", seen)
	}

	_, version := teacher.publishedVersion("Đề có cảnh báo " + nonce(t))
	assignment, attempt := flaggedAttempt(t, teacher, student, version, class)
	staff = teacher.must(http.StatusOK, http.MethodGet, staffThread, nil)
	events := objects(staff["contextEvents"])
	if len(events) != 1 || events[0]["attemptId"] != attempt || events[0]["assignmentId"] != assignment || events[0]["focusLost"] != float64(2) ||
		!strings.HasPrefix(events[0]["title"].(string), "Đề có cảnh báo") {
		t.Errorf("the teacher's context events: %v, want the flagged attempt", staff["contextEvents"])
	}
	for _, row := range objects(teacher.must(http.StatusOK, http.MethodGet, "/teacher/messages/threads?tab=direct", nil)["threads"]) {
		if row["thread"].(map[string]any)["studentId"] == studentID && row["unread"] != float64(1) {
			t.Errorf("the teacher's unread: %v, want the student's one message: a context event is never unread", row["unread"])
		}
	}
	teacher.must(http.StatusNoContent, http.MethodDelete, "/teacher/classes/"+class+"/members/"+studentID, nil)
	left := student.must(http.StatusOK, http.MethodGet, thread, nil)
	if head := left["teacher"].(map[string]any); head["former"] != true || len(head["classNames"].([]any)) != 0 || head["canPost"] != true {
		t.Errorf("the thread after the student left the class: %v, want 'Former teacher', still open", head)
	}
	student.must(http.StatusCreated, http.MethodPost, thread, map[string]any{"clientId": uuid.NewString(), "body": "Em vẫn hỏi cô được ạ?"})
	if status, _ := student.call(http.MethodGet, "/app/messages/classes/"+class, nil); status != http.StatusNotFound {
		t.Errorf("the channel of a class the student left: %d, want 404", status)
	}

	admin.must(http.StatusOK, http.MethodPost, "/admin/users/"+teacherID+"/disable", nil)
	if status, got := student.call(http.MethodPost, thread, map[string]any{"clientId": uuid.NewString(), "body": "Cô ơi?"}); status != http.StatusConflict || errorCode(got) != "MESSAGING_NOT_ALLOWED" {
		t.Errorf("a post to a disabled teacher: %d %v, want 409 MESSAGING_NOT_ALLOWED", status, got)
	}
	if head := student.must(http.StatusOK, http.MethodGet, thread, nil)["teacher"].(map[string]any); head["canPost"] != false {
		t.Errorf("the disabled teacher's thread: %v, want it readable with no post", head)
	}
}

func stringsOf(v any) []string {
	raw, _ := v.([]any)
	out := make([]string, len(raw))
	for i, s := range raw {
		out[i], _ = s.(string)
	}
	return out
}
