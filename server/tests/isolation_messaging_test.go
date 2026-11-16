//go:build e2e

package e2e

import (
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
