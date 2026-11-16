//go:build e2e

package e2e

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"
)

// directThreadsStayWithTheirParticipants is the DM case of the isolation
// suite (D17, DG-08). Teachers A and B both teach the shared student, in
// different classes, and each writes to them. A thread is addressed by its
// student, so B's address is B's own thread: B never sees A's message, and
// naming it as B's read pointer answers as a missing message does. The Admin,
// whom scope.all does not let into a direct thread, gets 404 on the student
// wherever a missing student answers 404, and still reads A's class channel,
// which is class content.
func (x *iso) directThreadsStayWithTheirParticipants() {
	x.t.Helper()
	student := "/teacher/messages/students/" + x.shared
	missing := "/teacher/messages/students/" + uuid.NewString()
	post := func(body string) *payload {
		return new(jsonPayload(x.t, map[string]any{"clientId": uuid.NewString(), "body": body}))
	}
	theirs := x.a.c.send(http.MethodPost, student, post("Tin riêng của giáo viên A"))
	mine := x.b.c.send(http.MethodPost, student, post("Tin riêng của giáo viên B"))
	if theirs.status != http.StatusCreated || mine.status != http.StatusCreated {
		x.t.Fatalf("both teachers writing to the student they share: A %s, B %s", answer(theirs), answer(mine))
	}
	theirID, myID := theirs.json["id"].(string), mine.json["id"].(string)

	for who, c := range map[string]struct {
		viewer       *client
		own, foreign string
		text         string
	}{
		"B": {x.b.c, myID, theirID, "giáo viên A"},
		"A": {x.a.c, theirID, myID, "giáo viên B"},
	} {
		read := c.viewer.send(http.MethodGet, student, nil)
		messages, _ := read.json["messages"].([]any)
		if read.status != http.StatusOK || len(messages) != 1 || id(messages[0].(map[string]any)) != c.own {
			x.t.Errorf("%s reading their thread with the shared student: %s, want their own message alone", who, answer(read))
		}
		listed := c.viewer.send(http.MethodGet, "/teacher/messages/threads?tab=direct", nil)
		for _, got := range []sent{read, listed} {
			if strings.Contains(string(got.body), c.foreign) || strings.Contains(string(got.body), c.text) {
				x.t.Errorf("%s sees the other teacher's thread with the shared student: %s", who, got.body)
			}
		}
		pointer := func(message string) sent {
			return c.viewer.send(http.MethodPost, student+"/read", new(jsonPayload(x.t, map[string]any{"lastReadMessageId": message})))
		}
		if foreign, absent := pointer(c.foreign), pointer(uuid.NewString()); foreign.status != http.StatusUnprocessableEntity || answer(foreign) != answer(absent) {
			x.t.Errorf("%s naming the other teacher's message as their pointer: %s, where a missing message answers %s", who, answer(foreign), answer(absent))
		}
	}

	for _, c := range []struct {
		method, path string
		body         func() *payload
	}{
		{http.MethodGet, "", func() *payload { return nil }},
		{http.MethodPost, "", func() *payload { return post("Tin của quản trị") }},
		{http.MethodPost, "/read", func() *payload {
			return new(jsonPayload(x.t, map[string]any{"lastReadMessageId": theirID}))
		}},
	} {
		got, absent := x.admin.send(c.method, student+c.path, c.body()), x.admin.send(c.method, missing+c.path, c.body())
		if got.status != http.StatusNotFound || answer(got) != answer(absent) {
			x.t.Errorf("the Admin's %s %s on the shared student: %s, want 404 as for a missing student (%s)", c.method, c.path, answer(got), answer(absent))
		}
		if strings.Contains(string(got.body), "giáo viên") {
			x.t.Errorf("the Admin's %s %s shows a message: %s", c.method, c.path, got.body)
		}
	}
	if listed := x.admin.send(http.MethodGet, "/teacher/messages/threads?tab=all", nil); listed.status != http.StatusOK ||
		strings.Contains(string(listed.body), x.shared) || strings.Contains(string(listed.body), "Tin riêng") {
		x.t.Errorf("the Admin's list names the shared student's threads: %s", answer(listed))
	}
	if channel := x.admin.send(http.MethodGet, "/teacher/messages/classes/"+x.a.id("class"), nil); channel.status != http.StatusOK {
		x.t.Errorf("the Admin reading the teacher's class channel: %s, want 200: a channel is class content", answer(channel))
	}
}

// studentThreadsStayWithTheirParticipants is the student-pair case of the
// isolation suite for /app/messages (T-R7.10b, R2's two-student pair). A
// student's thread is addressed by its teacher, so two students of one
// teacher each reach their own thread at the same address: neither sees the
// other's messages, and naming one as a pointer answers as a missing message
// does. A's student names B's teacher as a missing teacher, and B's class as
// a missing class. A student who left B's class with no thread cannot start
// one, as for a missing teacher, while one with a thread keeps writing. A
// channel takes no post from a student.
func (x *iso) studentThreadsStayWithTheirParticipants() {
	x.t.Helper()
	b := x.b
	thread := "/app/messages/teachers/" + b.userID
	missing := "/app/messages/teachers/" + uuid.NewString()
	post := func(body string) *payload {
		return new(jsonPayload(x.t, map[string]any{"clientId": uuid.NewString(), "body": body}))
	}
	pointer := func(message string) *payload {
		return new(jsonPayload(x.t, map[string]any{"lastReadMessageId": message}))
	}
	student := func(name string) (*client, string) {
		created := b.c.must(http.StatusCreated, http.MethodPost, "/teacher/students", map[string]any{
			"email": name + "-" + nonce(x.t) + "@example.com", "fullName": "Học viên " + name, "classIds": []string{b.id("class")},
		})
		user := created["user"].(map[string]any)
		return x.w.settledIn(user["email"].(string), created["temporaryPassword"].(string)), id(user)
	}

	other, otherID := student("cung-giao-vien")
	mine := b.student.c.send(http.MethodPost, thread, post("Tin của học viên thứ nhất"))
	theirs := other.send(http.MethodPost, thread, post("Tin của học viên thứ hai"))
	if mine.status != http.StatusCreated || theirs.status != http.StatusCreated {
		x.t.Fatalf("two students of one teacher writing to them: %s, %s", answer(mine), answer(theirs))
	}
	for who, c := range map[string]struct {
		viewer        *client
		own, foreign  string
		foreignText   string
		ownTextInList string
	}{
		"the first student":  {b.student.c, id(mine.json), id(theirs.json), "học viên thứ hai", "Tin của học viên thứ nhất"},
		"the second student": {other, id(theirs.json), id(mine.json), "học viên thứ nhất", "Tin của học viên thứ hai"},
	} {
		read := c.viewer.send(http.MethodGet, thread, nil)
		messages, _ := read.json["messages"].([]any)
		if read.status != http.StatusOK || len(messages) == 0 || id(messages[len(messages)-1].(map[string]any)) != c.own {
			x.t.Errorf("%s reading their thread with the teacher: %s, want their own message last", who, answer(read))
		}
		listed := c.viewer.send(http.MethodGet, "/app/messages/threads", nil)
		if !strings.Contains(string(listed.body), c.ownTextInList) {
			x.t.Errorf("%s's list does not preview their own message: %s", who, listed.body)
		}
		for _, got := range []sent{read, listed} {
			if strings.Contains(string(got.body), c.foreign) || strings.Contains(string(got.body), c.foreignText) {
				x.t.Errorf("%s sees the other student's thread with the same teacher: %s", who, got.body)
			}
		}
		foreign, absent := c.viewer.send(http.MethodPost, thread+"/read", pointer(c.foreign)), c.viewer.send(http.MethodPost, thread+"/read", pointer(uuid.NewString()))
		if foreign.status != http.StatusUnprocessableEntity || answer(foreign) != answer(absent) {
			x.t.Errorf("%s naming the other student's message as their pointer: %s, where a missing message answers %s", who, answer(foreign), answer(absent))
		}
	}

	refused := func(who string, c *client, path, missingPath string) {
		x.t.Helper()
		for _, r := range []struct {
			method, suffix string
			body           func() *payload
		}{
			{http.MethodGet, "", func() *payload { return nil }},
			{http.MethodPost, "", func() *payload { return post("Em chào thầy cô") }},
			{http.MethodPost, "/read", func() *payload { return pointer(id(mine.json)) }},
		} {
			got, absent := c.send(r.method, path+r.suffix, r.body()), c.send(r.method, missingPath+r.suffix, r.body())
			if got.status != http.StatusNotFound || answer(got) != answer(absent) {
				x.t.Errorf("%s: %s %s answered %s, want 404 as for a missing teacher (%s)", who, r.method, path+r.suffix, answer(got), answer(absent))
			}
		}
	}
	refused("A's student and B's teacher", x.a.student.c, thread, missing)
	for _, suffix := range []string{"", "/read"} {
		method, body := http.MethodGet, (*payload)(nil)
		if suffix != "" {
			method, body = http.MethodPost, pointer(id(mine.json))
		}
		got := x.a.student.c.send(method, "/app/messages/classes/"+b.id("class")+suffix, body)
		absent := x.a.student.c.send(method, "/app/messages/classes/"+uuid.NewString()+suffix, body)
		if got.status != http.StatusNotFound || answer(got) != answer(absent) || strings.Contains(string(got.body), "Chào lớp") {
			x.t.Errorf("A's student reading B's class channel%s: %s, want 404 as for a missing class (%s)", suffix, answer(got), answer(absent))
		}
	}

	leaver, leaverID := student("roi-lop")
	b.c.must(http.StatusNoContent, http.MethodDelete, "/teacher/classes/"+b.id("class")+"/members/"+leaverID, nil)
	refused("a student who left the class with no thread", leaver, thread, missing)
	b.c.must(http.StatusNoContent, http.MethodDelete, "/teacher/classes/"+b.id("class")+"/members/"+otherID, nil)
	if kept := other.send(http.MethodPost, thread, post("Em đã rời lớp nhưng vẫn hỏi cô")); kept.status != http.StatusCreated {
		x.t.Errorf("a student who left the class with a thread writing on: %s, want 201", answer(kept))
	}

	for path, want := range map[string]int{
		"/app/messages/classes/" + b.id("class"):     http.StatusMethodNotAllowed,
		"/teacher/messages/classes/" + b.id("class"): http.StatusForbidden,
	} {
		if got := b.student.c.send(http.MethodPost, path, post("Em chào cả lớp")); got.status != want {
			x.t.Errorf("a student posting to their class's channel at %s: %s, want %d", path, answer(got), want)
		}
	}
	if read := b.student.c.must(http.StatusOK, http.MethodGet, "/app/messages/classes/"+b.id("class"), nil); strings.Contains(fmt.Sprint(read), "Em chào cả lớp") {
		x.t.Errorf("the channel holds the student's refused post: %v", read)
	}
}
