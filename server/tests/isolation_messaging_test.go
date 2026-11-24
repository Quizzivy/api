//go:build e2e

package e2e

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"
)

// directThreadsStayWithTheirParticipants is the DM case of the isolation
// suite (D17, DG-08), under F2's rule that any staff member may write to any
// student. Teachers A and B each write to a student S, once the shared
// student both teach and once A's own student, whom B does not teach. A
// thread is addressed by its student, so B's address is B's own thread: B
// never sees A's message, and naming it as B's read pointer answers as a
// missing message does. The Admin naming S reads the Admin's own thread,
// empty until the Admin writes, never a teacher's; the file a teacher's
// direct message holds answers the Admin 404 as a missing file does; and the
// Admin still reads A's class channel, which is class content.
func (x *iso) directThreadsStayWithTheirParticipants() {
	x.t.Helper()
	post := func(body string) *payload {
		return new(jsonPayload(x.t, map[string]any{"clientId": uuid.NewString(), "body": body}))
	}
	pointer := func(message string) *payload {
		return new(jsonPayload(x.t, map[string]any{"lastReadMessageId": message}))
	}
	for which, s := range map[string]string{"the shared student": x.shared, "A's own student": x.a.id("student")} {
		student := "/teacher/messages/students/" + s
		theirs := x.a.c.send(http.MethodPost, student, post("Tin riêng của giáo viên A cho "+which))
		mine := x.b.c.send(http.MethodPost, student, post("Tin riêng của giáo viên B cho "+which))
		if theirs.status != http.StatusCreated || mine.status != http.StatusCreated {
			x.t.Fatalf("both teachers writing to %s: A %s, B %s", which, answer(theirs), answer(mine))
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
				x.t.Errorf("%s reading their thread with %s: %s, want their own message alone", who, which, answer(read))
			}
			listed := c.viewer.send(http.MethodGet, "/teacher/messages/threads?tab=direct", nil)
			for _, got := range []sent{read, listed} {
				if strings.Contains(string(got.body), c.foreign) || strings.Contains(string(got.body), c.text) {
					x.t.Errorf("%s sees the other teacher's thread with %s: %s", who, which, got.body)
				}
			}
			mark := func(message string) sent { return c.viewer.send(http.MethodPost, student+"/read", pointer(message)) }
			if foreign, absent := mark(c.foreign), mark(uuid.NewString()); foreign.status != http.StatusUnprocessableEntity || answer(foreign) != answer(absent) {
				x.t.Errorf("%s naming the other teacher's message with %s as their pointer: %s, where a missing message answers %s", who, which, answer(foreign), answer(absent))
			}
		}

		if listed := x.admin.send(http.MethodGet, "/teacher/messages/threads?tab=all", nil); listed.status != http.StatusOK ||
			strings.Contains(string(listed.body), s) || strings.Contains(string(listed.body), "Tin riêng") {
			x.t.Errorf("the Admin's list names %s's threads: %s", which, answer(listed))
		}
		read := x.admin.send(http.MethodGet, student, nil)
		if messages, _ := read.json["messages"].([]any); read.status != http.StatusOK || len(messages) != 0 || read.json["latestId"] != nil ||
			strings.Contains(string(read.body), "Tin riêng") {
			x.t.Errorf("the Admin naming %s: %s, want 200 and the Admin's own empty thread", which, answer(read))
		}
		foreign, absent := x.admin.send(http.MethodPost, student+"/read", pointer(theirID)), x.admin.send(http.MethodPost, student+"/read", pointer(uuid.NewString()))
		if foreign.status != http.StatusUnprocessableEntity || answer(foreign) != answer(absent) {
			x.t.Errorf("the Admin naming A's message with %s as a pointer: %s, where a missing message answers %s", which, answer(foreign), answer(absent))
		}
		written := x.admin.send(http.MethodPost, student, post("Tin của quản trị cho "+which))
		if written.status != http.StatusCreated {
			x.t.Fatalf("the Admin writing to %s: %s", which, answer(written))
		}
		if read = x.admin.send(http.MethodGet, student, nil); strings.Contains(string(read.body), "Tin riêng") || len(read.json["messages"].([]any)) != 1 {
			x.t.Errorf("the Admin's thread with %s after writing: %s, want the Admin's message alone", which, answer(read))
		}
		for who, viewer := range map[string]*client{"A": x.a.c, "B": x.b.c} {
			if got := viewer.send(http.MethodGet, student, nil); strings.Contains(string(got.body), "Tin của quản trị") {
				x.t.Errorf("%s sees the Admin's thread with %s: %s", who, which, got.body)
			}
		}
	}
	for who, file := range map[string]string{"A": x.a.id("attachment"), "B": x.b.id("attachment")} {
		got, absent := x.admin.send(http.MethodGet, "/teacher/messages/attachments/"+file, nil), x.admin.send(http.MethodGet, "/teacher/messages/attachments/"+uuid.NewString(), nil)
		if got.status != http.StatusNotFound || answer(got) != answer(absent) {
			x.t.Errorf("the Admin downloading the file of %s's direct message: %s, want 404 as for a missing file (%s)", who, answer(got), answer(absent))
		}
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

// colleagueThreadsStayWithTheirParticipants is the colleague case of the
// isolation suite (F2, T-F2.4, D17): teachers A and B share a thread with a
// file. Teacher C naming A reads C's own empty thread, never A↔B; C's list
// shows none of it; a pointer naming A↔B's message answers as a missing
// message does; the file answers C as a missing file does. The Admin, whose
// scope.all opens no thread, is C's like: naming A reads the Admin's own
// thread, and once the Admin writes, A reads the Admin's message in the
// Admin's thread alone and B never sees it. A student session is refused
// the colleague operations by audience.
func (x *iso) colleagueThreadsStayWithTheirParticipants() {
	x.t.Helper()
	post := func(body string, file ...string) *payload {
		p := map[string]any{"clientId": uuid.NewString(), "body": body}
		if len(file) > 0 {
			p["attachmentId"] = file[0]
		}
		return new(jsonPayload(x.t, p))
	}
	pointer := func(message string) *payload {
		return new(jsonPayload(x.t, map[string]any{"lastReadMessageId": message}))
	}
	file := uploadAttachment(x.t, x.a.c, "rieng-a-b.pdf")
	sent := x.a.c.send(http.MethodPost, "/teacher/messages/colleagues/"+x.b.userID, post("Tin riêng của A cho B", file))
	if sent.status != http.StatusCreated {
		x.t.Fatalf("A writing to B: %s", answer(sent))
	}
	theirs := id(sent.json)
	if got := x.b.c.send(http.MethodGet, "/teacher/messages/colleagues/"+x.a.userID, nil); got.status != http.StatusOK || !strings.Contains(string(got.body), theirs) {
		x.t.Errorf("B reading their thread with A: %s, want A's message", answer(got))
	}
	if got := x.b.c.send(http.MethodGet, "/teacher/messages/attachments/"+file, nil); got.status != http.StatusOK {
		x.t.Errorf("B downloading A's file: %s, want 200", answer(got))
	}

	c := x.w.signedIn(x.w.createStaff("teacher"))
	for who, viewer := range map[string]*client{"C": c, "the Admin": x.admin} {
		read := viewer.send(http.MethodGet, "/teacher/messages/colleagues/"+x.a.userID, nil)
		if messages, _ := read.json["messages"].([]any); read.status != http.StatusOK || len(messages) != 0 || read.json["latestId"] != nil ||
			strings.Contains(string(read.body), "Tin riêng") {
			x.t.Errorf("%s naming A: %s, want 200 and their own empty thread", who, answer(read))
		}
		for _, path := range []string{"/teacher/messages/colleagues", "/teacher/messages/colleagues?q=a", "/teacher/messages/threads?tab=all"} {
			if listed := viewer.send(http.MethodGet, path, nil); listed.status != http.StatusOK || strings.Contains(string(listed.body), theirs) ||
				strings.Contains(string(listed.body), "Tin riêng của A") {
				x.t.Errorf("%s's %s shows A↔B: %s", who, path, answer(listed))
			}
		}
		for _, colleague := range []string{x.a.userID, x.b.userID} {
			path := "/teacher/messages/colleagues/" + colleague + "/read"
			foreign, absent := viewer.send(http.MethodPost, path, pointer(theirs)), viewer.send(http.MethodPost, path, pointer(uuid.NewString()))
			if foreign.status != http.StatusUnprocessableEntity || answer(foreign) != answer(absent) {
				x.t.Errorf("%s naming A↔B's message as their pointer with %s: %s, where a missing message answers %s", who, colleague, answer(foreign), answer(absent))
			}
		}
		got, absent := viewer.send(http.MethodGet, "/teacher/messages/attachments/"+file, nil), viewer.send(http.MethodGet, "/teacher/messages/attachments/"+uuid.NewString(), nil)
		if got.status != http.StatusNotFound || answer(got) != answer(absent) {
			x.t.Errorf("%s downloading A↔B's file: %s, want 404 as for a missing file (%s)", who, answer(got), answer(absent))
		}
	}
	if written := x.admin.send(http.MethodPost, "/teacher/messages/colleagues/"+x.a.userID, post("Tin của quản trị cho A")); written.status != http.StatusCreated {
		x.t.Fatalf("the Admin writing to A: %s", answer(written))
	}
	if read := x.admin.send(http.MethodGet, "/teacher/messages/colleagues/"+x.a.userID, nil); strings.Contains(string(read.body), "Tin riêng") || len(read.json["messages"].([]any)) != 1 {
		x.t.Errorf("the Admin's thread with A after writing: %s, want the Admin's message alone", answer(read))
	}
	if got := x.b.c.send(http.MethodGet, "/teacher/messages/colleagues/"+x.a.userID, nil); strings.Contains(string(got.body), "Tin của quản trị") {
		x.t.Errorf("B sees the Admin's thread with A: %s", got.body)
	}
	for _, r := range []struct{ method, path string }{
		{http.MethodGet, "/teacher/messages/colleagues"},
		{http.MethodGet, "/teacher/messages/colleagues/" + x.a.userID},
		{http.MethodPost, "/teacher/messages/colleagues/" + x.a.userID},
		{http.MethodPost, "/teacher/messages/colleagues/" + x.a.userID + "/read"},
	} {
		var body *payload
		if r.method == http.MethodPost {
			body = post("Em chào cô")
			if strings.HasSuffix(r.path, "/read") {
				body = pointer(theirs)
			}
		}
		if got := x.b.student.c.send(r.method, r.path, body); got.status != http.StatusForbidden {
			x.t.Errorf("a student session at %s %s: %s, want 403 by audience", r.method, r.path, answer(got))
		}
	}
}
