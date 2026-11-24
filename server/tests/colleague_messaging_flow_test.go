//go:build e2e

package e2e

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// named renames the account c signed in as, so a search tells staff apart:
// createStaff names every account alike.
func (w *world) named(c *client, name string) string {
	w.t.Helper()
	me := id(c.must(http.StatusOK, http.MethodGet, "/auth/me", nil))
	if _, err := w.pool.Exec(context.Background(), `UPDATE app.users SET full_name = $2 WHERE id = $1::uuid`, me, name); err != nil {
		w.t.Fatal(err)
	}
	return me
}

func (w *world) auditRowsBy(userIDs ...string) int {
	w.t.Helper()
	var n int
	if err := w.pool.QueryRow(context.Background(), `SELECT count(*)::int FROM app.audit_log WHERE actor_user_id = ANY($1::uuid[])`, userIDs).Scan(&n); err != nil {
		w.t.Fatal(err)
	}
	return n
}

// TestTwoColleaguesWriteShareAFileAndAreToldOfIt is T-F2.4 through HTTP:
// teacher A writes first to teacher B with a PDF (201, then 200 for the same
// clientId); B lists the thread in the Colleagues tab with A's role and one
// unread, counts it in the nav, holds a message.received item leading to A,
// downloads the file, and is emailed ten minutes later under the New message
// switch, naming A and no class, linking to the colleague thread on the
// staff host, never quoting the message; B's reply clears B's count. A
// colleague whose email column is off is told in the app alone. A disabled
// colleague's thread stays readable and answers a post 409. A student is
// refused the colleague operations. Nothing is audited.
func TestTwoColleaguesWriteShareAFileAndAreToldOfIt(t *testing.T) {
	w := bootWithStorage(t)
	tag := nonce(t)
	aEmail, aPassword := w.createStaff("teacher")
	a := w.signedIn(aEmail, aPassword)
	bEmail, bPassword := w.createStaff("teacher")
	b := w.signedIn(bEmail, bPassword)
	quiet := w.signedIn(w.createStaff("teacher"))
	aID, bID, quietID := w.named(a, "Cô Ánh "+tag), w.named(b, "Thầy Bình "+tag), w.named(quiet, "Cô Yên "+tag)
	audited := w.auditRowsBy(aID, bID)

	file := uploadAttachment(t, a, "de-nghe-"+tag+".pdf")
	thread := "/teacher/messages/colleagues/" + bID
	post := map[string]any{"clientId": uuid.NewString(), "body": "Chị gửi em đề nghe bí mật tuần sau.", "attachmentId": file}
	before := time.Now()
	sent := a.must(http.StatusCreated, http.MethodPost, thread, post)
	if again := a.must(http.StatusOK, http.MethodPost, thread, post); id(again) != id(sent) {
		t.Errorf("the repeated clientId answered %v, want the message it made", again)
	}
	if sender := sent["sender"].(map[string]any); sender["side"] != "staff" || sent["mine"] != true || sent["attachment"] == nil {
		t.Errorf("the post answered %v, want A's own staff message with the file", sent)
	}

	rows := objects(b.must(http.StatusOK, http.MethodGet, "/teacher/messages/colleagues", nil)["threads"])
	if len(rows) != 1 || rows[0]["colleagueId"] != aID || rows[0]["title"] != "Cô Ánh "+tag || rows[0]["unread"] != float64(1) ||
		rows[0]["roleBuiltinKey"] != "teacher" || rows[0]["roleName"] == nil || rows[0]["candidate"] != false || rows[0]["disabled"] != false {
		t.Fatalf("B's Colleagues tab: %v, want A's thread with A's role and one unread", rows)
	}
	var keys []string
	for k := range rows[0] {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	if want := []string{"avatarUrl", "candidate", "colleagueId", "disabled", "lastMessageAt", "preview", "roleBuiltinKey", "roleName", "title", "unread"}; !slices.Equal(keys, want) {
		t.Errorf("a Colleagues row carries %v, want exactly %v: never an email", keys, want)
	}
	if got := b.unreadMessages(t, "/teacher/summary"); got != float64(1) {
		t.Errorf("B's nav counts %v, want A's message", got)
	}
	if got := a.unreadMessages(t, "/teacher/summary"); got != float64(0) {
		t.Errorf("A's nav counts %v, want 0: never their own", got)
	}
	items := b.messageItems(t, "teacher")
	if len(items) != 1 || items[0]["kind"] != "message.received" || items[0]["target"].(map[string]any)["colleagueId"] != aID ||
		items[0]["target"].(map[string]any)["studentId"] != nil || items[0]["params"].(map[string]any)["className"] != nil {
		t.Errorf("B's bell holds %v, want one message.received leading to A and naming no class", items)
	}

	read := b.must(http.StatusOK, http.MethodGet, "/teacher/messages/colleagues/"+aID, nil)
	peer := read["colleague"].(map[string]any)
	messages := objects(read["messages"])
	if peer["id"] != aID || peer["canPost"] != true || peer["roleBuiltinKey"] != "teacher" || len(messages) != 1 || id(messages[0]) != id(sent) ||
		messages[0]["mine"] != false || read["latestId"] != id(sent) || read["contextEvents"] != nil {
		t.Errorf("B reading the thread: %v", read)
	}
	if _, leaked := peer["email"]; leaked {
		t.Errorf("the colleague's header carries an email: %v", peer)
	}
	if url := b.must(http.StatusOK, http.MethodGet, "/teacher/messages/attachments/"+file, nil); url["url"] == nil {
		t.Errorf("B downloading A's file: %v", url)
	}

	queued := w.messageEmails(t, bID)
	if len(queued) != 1 || queued[0].deliverAfter.Before(before.Add(10*time.Minute)) || strings.Contains(queued[0].params, "bí mật") ||
		!strings.Contains(queued[0].params, aID) {
		t.Fatalf("A's message queued B %+v, want one email ten minutes out, with the thread's ids and no word of the message", queued)
	}
	d, sender := w.dispatcherAt(t, time.Now().Add(11*time.Minute))
	drain(t, d)
	mails := sender.to(bEmail)
	if len(mails) != 1 {
		t.Fatalf("B was emailed %d times, want once", len(mails))
	}
	m := mails[0]
	for _, part := range []string{m.Subject, m.Text, m.HTML} {
		if strings.Contains(part, "bí mật") || strings.Contains(part, "de-nghe") {
			t.Errorf("the email carries the message or its file: %s", part)
		}
	}
	if !strings.Contains(m.Subject, "Cô Ánh "+tag) || !strings.Contains(m.Text, "/teacher/messages/colleagues/"+aID+"\n") ||
		strings.Contains(m.Text, "trong lớp") || strings.Contains(m.Text, "announcement") {
		t.Errorf("the email does not name A, link to the colleague thread, or names a class:\n%s\n%s", m.Subject, m.Text)
	}

	reply := b.must(http.StatusCreated, http.MethodPost, "/teacher/messages/colleagues/"+aID, map[string]any{"clientId": uuid.NewString(), "body": "Dạ em cảm ơn chị."})
	b.must(http.StatusNoContent, http.MethodPost, "/teacher/messages/colleagues/"+aID+"/read", map[string]any{"lastReadMessageId": id(reply)})
	if got := b.unreadMessages(t, "/teacher/summary"); got != float64(0) {
		t.Errorf("B's nav after reading counts %v", got)
	}
	if got := a.unreadMessages(t, "/teacher/summary"); got != float64(1) {
		t.Errorf("A's nav after B's reply counts %v, want 1", got)
	}

	switches := quiet.must(http.StatusOK, http.MethodGet, "/me/notification-preferences", nil)["items"].([]any)
	for _, item := range switches {
		if s := item.(map[string]any); s["event"] == "message.received" {
			s["inApp"], s["email"] = true, false
		}
	}
	quiet.must(http.StatusOK, http.MethodPut, "/me/notification-preferences", switches)
	a.must(http.StatusCreated, http.MethodPost, "/teacher/messages/colleagues/"+quietID, map[string]any{"clientId": uuid.NewString(), "body": "Chào chị Yên."})
	if got := w.messageEmails(t, quietID); len(got) != 0 {
		t.Errorf("a colleague who turned the email of New message off is queued %+v", got)
	}
	if items := quiet.messageItems(t, "teacher"); len(items) != 1 {
		t.Errorf("that colleague's bell holds %v, want the in-app item", items)
	}

	if _, err := w.pool.Exec(context.Background(), `UPDATE app.users SET disabled_at = now() WHERE id = $1::uuid`, bID); err != nil {
		t.Fatal(err)
	}
	held := a.must(http.StatusOK, http.MethodGet, thread, nil)
	if c := held["colleague"].(map[string]any); c["disabled"] != true || c["canPost"] != false || len(objects(held["messages"])) != 2 {
		t.Errorf("A reading the disabled colleague's thread: %v, want it readable, disabled and closed to posts", held)
	}
	if status, body := a.call(http.MethodPost, thread, map[string]any{"clientId": uuid.NewString(), "body": "Em còn đó không?"}); status != http.StatusConflict ||
		errorCode(body) != "MESSAGING_NOT_ALLOWED" {
		t.Errorf("a post to the disabled colleague: %d %v, want 409 MESSAGING_NOT_ALLOWED", status, body)
	}
	if rows := objects(a.must(http.StatusOK, http.MethodGet, "/teacher/messages/colleagues?q="+tag, nil)["threads"]); len(rows) != 2 ||
		rows[0]["colleagueId"] != quietID || rows[1]["colleagueId"] != bID || rows[1]["disabled"] != true {
		t.Errorf("A's tab searched: %v, want the thread with Yên, then the disabled colleague's, and no candidate", rows)
	}

	if got := w.auditRowsBy(aID, bID); got != audited {
		t.Errorf("colleague posts, reads and uploads wrote %d audit rows, want none", got-audited)
	}

	created := a.must(http.StatusCreated, http.MethodPost, "/teacher/students", map[string]any{
		"email": "dong-nghiep-hv-" + nonce(t) + "@example.com", "fullName": "Học viên " + tag, "classIds": []string{},
	})
	learner := w.settledIn(created["user"].(map[string]any)["email"].(string), created["temporaryPassword"].(string))
	for _, r := range []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, "/teacher/messages/colleagues", nil},
		{http.MethodGet, "/teacher/messages/colleagues/" + aID, nil},
		{http.MethodPost, "/teacher/messages/colleagues/" + aID, map[string]any{"clientId": uuid.NewString(), "body": "Em chào cô"}},
		{http.MethodPost, "/teacher/messages/colleagues/" + aID + "/read", map[string]any{"lastReadMessageId": id(sent)}},
		{http.MethodGet, "/teacher/messages/recipients?audience=colleagues", nil},
	} {
		if status, _ := learner.call(r.method, r.path, r.body); status != http.StatusForbidden {
			t.Errorf("a student at %s %s: %d, want 403", r.method, r.path, status)
		}
	}
	if status, _ := a.call(http.MethodGet, "/teacher/messages/colleagues/"+id(created["user"].(map[string]any)), nil); status != http.StatusNotFound {
		t.Errorf("a teacher naming a student as a colleague: %d, want 404", status)
	}
}
