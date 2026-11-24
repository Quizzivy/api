//go:build integration

package application_test

import (
	"context"
	"encoding/base64"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"quizzivy/internal/core/adapters"
	"quizzivy/internal/modules/messaging/application/command"
	"quizzivy/internal/modules/messaging/application/query"
	"quizzivy/internal/modules/messaging/domain"
	messagingrepo "quizzivy/internal/modules/messaging/repositories"
	notificationsapp "quizzivy/internal/modules/notifications/application"
	notificationsrepo "quizzivy/internal/modules/notifications/repositories"
	"quizzivy/internal/platform/db"
	"quizzivy/internal/shared/actor"
)

// role makes a custom role holding keys.
func (w *world) role(name string, keys ...string) string {
	w.t.Helper()
	role := w.id(`INSERT INTO app.roles (name, icon, color) VALUES ($1, 'user', 'gray') RETURNING id::text`, name+" "+w.marker)
	for _, key := range keys {
		w.exec(`INSERT INTO app.role_permissions (role_id, permission_key) VALUES ($1::uuid, $2)`, role, key)
	}
	return role
}

// holding makes an account of the role with the id role.
func (w *world) holding(name, role string) string {
	w.t.Helper()
	return w.id(`INSERT INTO app.users (email, full_name, role_id, avatar_key) VALUES ($1, $2, $3::uuid, 'avatars/' || $1) RETURNING id::text`,
		w.marker+"-"+uuid.NewString()[:8]+"@example.test", name+" "+w.marker, role)
}

func (w *world) toColleague(who actor.Actor, colleague, body string) (domain.Posted, error) {
	w.t.Helper()
	return w.app.Commands.PostColleague.Handle(context.Background(), command.PostColleague{
		Actor: who, ColleagueID: colleague, ClientID: uuid.NewString(), Body: text(body),
	})
}

func (w *world) mustToColleague(who actor.Actor, colleague, body string) domain.Message {
	w.t.Helper()
	posted, err := w.toColleague(who, colleague, body)
	if err != nil {
		w.t.Fatalf("%s writing to %s: %v", who.ID, colleague, err)
	}
	return posted.Message
}

func (w *world) colleagueThread(who, colleague string, cursor domain.Cursor) (query.ColleagueDirect, error) {
	w.t.Helper()
	return w.app.Queries.ColleagueThread.Handle(context.Background(), query.ColleagueThread{ActorID: who, ColleagueID: colleague, Cursor: cursor})
}

// colleagues is who's Colleagues tab searched by q, kept to the rows this
// test made.
func (w *world) colleagues(who, q string) []domain.ColleagueThread {
	w.t.Helper()
	threads, err := w.app.Queries.ColleagueThreads.Handle(context.Background(), query.ColleagueThreads{ActorID: who, Query: q})
	if err != nil {
		w.t.Fatal(err)
	}
	var out []domain.ColleagueThread
	for _, t := range threads {
		if strings.Contains(t.Title, w.marker) {
			out = append(out, t)
		}
	}
	return out
}

func (w *world) colleagueRecipients(who actor.Actor, f domain.RecipientFilter, cursor string) (domain.Recipients, error) {
	w.t.Helper()
	f.ActorID, f.All = who.ID, who.Scope.All
	return w.app.Queries.ColleagueRecipients.Handle(context.Background(), query.ColleagueRecipients{Filter: f, Cursor: cursor})
}

func messageIDs(messages []domain.Message) []string {
	out := make([]string, len(messages))
	for i, m := range messages {
		out[i] = m.ID
	}
	return out
}

// TestTheColleagueTableOnTheDatabase is F2's colleague rule on the database:
// a thread, or an enabled account that opens the teacher workspace and is
// not a student, anonymised or the caller. A teacher, an Admin, an
// assistant and a custom staff role with the workspace are colleagues; an
// account that opens only the Admin console, a student, a disabled account
// with no thread, an anonymised one and the caller are not (404); a thread
// stays readable after its colleague is disabled or loses the workspace, and
// takes no post (409). The picker lists exactly the accounts a post goes to,
// so its SQL is the manager's CanPostColleague, row by row.
func TestTheColleagueTableOnTheDatabase(t *testing.T) {
	w := newWorld(t)
	teacher, other, admin := w.user("Cô Lan", "teacher"), w.user("Thầy Minh", "teacher"), w.user("Quản trị", "admin")
	lan := by(teacher)
	assistant := w.user("Trợ giảng", "assistant")
	custom := w.holding("Giáo vụ", w.role("Giáo vụ", "people.students.read"))
	consoleOnly := w.holding("Kiểm toán", w.role("Kiểm toán", "system.audit.read"))
	student := w.user("Học viên", "student")
	disabled := w.user("Giáo viên đã nghỉ", "teacher")
	w.disable(disabled)
	disabledWithThread := w.user("Giáo viên nghỉ sau khi nhắn", "teacher")
	w.mustToColleague(lan, disabledWithThread, "Chị gửi em đề.")
	w.disable(disabledWithThread)
	movedToConsole := w.user("Giáo viên chuyển sang kiểm toán", "teacher")
	w.mustToColleague(lan, movedToConsole, "Chào anh.")
	w.exec(`UPDATE app.users SET role_id = (SELECT role_id FROM app.users WHERE id = $2::uuid) WHERE id = $1::uuid`, movedToConsole, consoleOnly)
	anonymised := w.user("Giáo viên đã ẩn danh", "teacher")
	w.anonymise(anonymised)
	anonymisedWithThread := w.user("Giáo viên ẩn danh còn cuộc trò chuyện", "teacher")
	w.mustToColleague(lan, anonymisedWithThread, "Chào chị.")
	w.anonymise(anonymisedWithThread)

	cases := map[string]struct {
		who, colleague string
		read, post     error
		evidence       string
	}{
		"a teacher":                              {teacher, other, nil, nil, "opens the workspace"},
		"an Admin, for a teacher":                {admin, teacher, nil, nil, "the Admin's own thread; scope.all plays no part"},
		"a teacher, for the Admin":               {teacher, admin, nil, nil, "the Admin opens the workspace"},
		"an assistant":                           {teacher, assistant, nil, nil, "a built-in staff role"},
		"a custom staff role with the workspace": {teacher, custom, nil, nil, "a key that opens the workspace"},
		"a role with only the Admin console":     {teacher, consoleOnly, domain.ErrNotFound, domain.ErrNotFound, "cannot open Messages"},
		"a student":                              {teacher, student, domain.ErrNotFound, domain.ErrNotFound, "getStaffDirectThread is a student's thread"},
		"a disabled colleague without a thread":  {teacher, disabled, domain.ErrNotFound, domain.ErrNotFound, "a disabled account only through a thread"},
		"a disabled colleague with a thread":     {teacher, disabledWithThread, nil, domain.ErrNotAllowed, "the thread stays readable"},
		"a disabled colleague, for a third":      {other, disabledWithThread, domain.ErrNotFound, domain.ErrNotFound, "the thread is the first teacher's"},
		"a colleague who lost the workspace":     {teacher, movedToConsole, nil, domain.ErrNotAllowed, "the thread stays readable"},
		"an anonymised account":                  {teacher, anonymised, domain.ErrNotFound, domain.ErrNotFound, "never a colleague"},
		"an anonymised account a thread names":   {teacher, anonymisedWithThread, domain.ErrNotFound, domain.ErrNotFound, "never a colleague, a thread or not"},
		"the caller naming themselves":           {teacher, teacher, domain.ErrNotFound, domain.ErrNotFound, "no thread with oneself"},
		"an id no account has":                   {teacher, uuid.NewString(), domain.ErrNotFound, domain.ErrNotFound, "missing"},
	}
	repo := messagingrepo.NewPostgres(db.NewContext(w.tx))
	for name, c := range cases {
		facts, err := repo.ColleagueFacts(context.Background(), c.who, c.colleague)
		if err != nil {
			t.Fatal(err)
		}
		if got := (domain.Eligibility{}).Colleague(facts); got != (c.read == nil) {
			t.Errorf("%s: the facts %+v make Colleague %v, want %v", name, facts, got, c.read == nil)
		}
		thread, err := w.colleagueThread(c.who, c.colleague, domain.Cursor{})
		wantErr(t, name+": read ("+c.evidence+")", err, c.read)
		if err == nil && thread.Peer.CanPost != (c.post == nil) {
			t.Errorf("%s: canPost = %v, want %v", name, thread.Peer.CanPost, c.post == nil)
		}
		_, err = w.toColleague(byAdminIf(c.who, admin), c.colleague, "Chào anh chị.")
		wantErr(t, name+": post ("+c.evidence+")", err, c.post)
	}
	for name, c := range cases {
		page, err := w.colleagueRecipients(byAdminIf(c.who, admin), domain.RecipientFilter{Query: w.marker, Limit: domain.MaxRecipients}, "")
		if err != nil {
			t.Fatal(err)
		}
		if listed := slices.Contains(ids(page.Rows), c.colleague); listed != (c.post == nil) {
			t.Errorf("%s: the picker lists the account: %v, want %v: the picker is whom a post goes to", name, listed, c.post == nil)
		}
	}
}

// TestTheFirstColleaguePostMakesOneThreadForThePair: either colleague may
// write first, and the thread they share is one, its pair stored low-first
// with no student, staff member or class; each post moves last_message_at; a
// repeated clientId answers the message it made, and one already used in
// another thread is refused.
func TestTheFirstColleaguePostMakesOneThreadForThePair(t *testing.T) {
	w := newWorld(t)
	a, b, c := w.user("Cô An", "teacher"), w.user("Thầy Bảo", "teacher"), w.user("Cô Chi", "teacher")
	ctx := context.Background()

	if got := w.count(`SELECT count(*)::int FROM app.conversations WHERE kind = 'colleague' AND $1::uuid IN (colleague_low_id, colleague_high_id)`, a); got != 0 {
		t.Fatalf("A starts with %d colleague threads", got)
	}
	if _, err := w.colleagueThread(a, b, domain.Cursor{}); err != nil {
		t.Fatal(err)
	}
	if got := w.count(`SELECT count(*)::int FROM app.conversations WHERE kind = 'colleague' AND $1::uuid IN (colleague_low_id, colleague_high_id)`, a); got != 0 {
		t.Errorf("reading made %d threads: the first post makes it", got)
	}

	clientID := uuid.NewString()
	first, err := w.app.Commands.PostColleague.Handle(ctx, command.PostColleague{Actor: by(b), ColleagueID: a, ClientID: clientID, Body: text("Em gửi chị đề nghe.")})
	if err != nil || !first.Created {
		t.Fatalf("B's first post to A: %+v, %v", first, err)
	}
	replay, err := w.app.Commands.PostColleague.Handle(ctx, command.PostColleague{Actor: by(b), ColleagueID: a, ClientID: clientID, Body: text("Em gửi chị đề nghe.")})
	if err != nil || replay.Created || replay.Message.ID != first.Message.ID {
		t.Errorf("B's post repeated: %+v, %v, want the message the first call made", replay, err)
	}
	reply := w.mustToColleague(by(a), b, "Chị cảm ơn em.")
	if reply.ConversationID != first.Message.ConversationID {
		t.Errorf("A's reply went to thread %s, B's post to %s: the pair has one thread", reply.ConversationID, first.Message.ConversationID)
	}
	_, err = w.app.Commands.PostColleague.Handle(ctx, command.PostColleague{Actor: by(b), ColleagueID: c, ClientID: clientID, Body: text("Chào chị Chi.")})
	wantErr(t, "B's clientId for A's thread, sent to C", err, domain.ErrClientIDTaken)

	var low, high string
	var staff, student, class *string
	var last time.Time
	var threads int
	if err := w.tx.QueryRow(ctx, `
		SELECT v.colleague_low_id::text, v.colleague_high_id::text, v.staff_id::text, v.student_id::text, v.class_id::text, v.last_message_at,
		       (SELECT count(*)::int FROM app.conversations x WHERE x.kind = 'colleague' AND least($2::uuid, $3::uuid) = x.colleague_low_id AND greatest($2::uuid, $3::uuid) = x.colleague_high_id)
		  FROM app.conversations v WHERE v.id = $1::uuid AND v.kind = 'colleague'`, first.Message.ConversationID, a, b).
		Scan(&low, &high, &staff, &student, &class, &last, &threads); err != nil {
		t.Fatal(err)
	}
	want := []string{a, b}
	slices.Sort(want)
	if low != want[0] || high != want[1] || staff != nil || student != nil || class != nil || threads != 1 {
		t.Errorf("the thread stores the pair %s, %s (staff %v, student %v, class %v; %d threads), want %v low-first and nothing else", low, high, staff, student, class, threads, want)
	}
	if last.Before(reply.CreatedAt) {
		t.Errorf("last_message_at %v is before the newest post %v", last, reply.CreatedAt)
	}

	for who, c := range map[string]struct{ reader, colleague, mine string }{
		"A": {a, b, reply.ID},
		"B": {b, a, first.Message.ID},
	} {
		thread, err := w.colleagueThread(c.reader, c.colleague, domain.Cursor{})
		if err != nil {
			t.Fatal(err)
		}
		if got := messageIDs(thread.Page.Messages); !slices.Equal(got, []string{first.Message.ID, reply.ID}) {
			t.Errorf("%s reads %v, want both messages oldest first", who, got)
		}
		for _, m := range thread.Page.Messages {
			if m.Side != domain.SideStaff || m.Mine != (m.ID == c.mine) || m.SeenBy != nil {
				t.Errorf("%s reads %+v: every sender is staff, Mine is the reader's own, and there is no Seen by", who, m)
			}
		}
		if thread.Page.LatestID == nil || *thread.Page.LatestID != reply.ID || thread.Peer.ID != c.colleague || !thread.Peer.CanPost ||
			thread.Peer.RoleName == "" || thread.Peer.RoleBuiltinKey == nil || *thread.Peer.RoleBuiltinKey != "teacher" {
			t.Errorf("%s's header and latest: %+v, %v", who, thread.Peer, thread.Page.LatestID)
		}
	}
}

// aged moves the thread conversation's activity back by ago: the world's
// one transaction gives every post the same now().
func (w *world) aged(conversation string, ago time.Duration) {
	w.t.Helper()
	w.exec(`UPDATE app.conversations SET last_message_at = last_message_at - $2::interval WHERE id = $1::uuid`, conversation, ago.String())
}

func (w *world) count(sql string, args ...any) int {
	w.t.Helper()
	var n int
	w.value(sql, &n, args...)
	return n
}

// TestAColleagueThreadIsReadOnlyByItsPair is D17 for colleague threads:
// teachers A and B share a thread; C naming A reads C's own empty thread,
// never A↔B, and so does the Admin, whose scope.all opens none; a pointer
// naming A↔B's message answers as a message of another thread; neither C's
// nor the Admin's list shows A↔B, and A's list shows A's threads alone.
func TestAColleagueThreadIsReadOnlyByItsPair(t *testing.T) {
	w := newWorld(t)
	a, b, c, admin := w.user("Cô An", "teacher"), w.user("Thầy Bảo", "teacher"), w.user("Cô Chi", "teacher"), w.user("Quản trị", "admin")
	fromA := w.mustToColleague(by(a), b, "Tin riêng của An cho Bảo")
	fromB := w.mustToColleague(by(b), a, "Tin riêng của Bảo cho An")
	w.aged(fromA.ConversationID, time.Hour)

	for who, reader := range map[string]actor.Actor{"C": by(c), "the Admin": byAdmin(admin)} {
		thread, err := w.colleagueThread(reader.ID, a, domain.Cursor{})
		if err != nil || len(thread.Page.Messages) != 0 || thread.Page.LatestID != nil {
			t.Errorf("%s naming A: %+v, %v, want their own empty thread", who, thread.Page, err)
		}
		wantErr(t, who+"'s pointer naming A's message to B", w.read(reader, domain.ThreadRef{Kind: domain.KindColleague, ColleagueID: a}, fromA.ID), domain.ErrNotInThread)
		wantErr(t, who+"'s pointer naming B's message to A", w.read(reader, domain.ThreadRef{Kind: domain.KindColleague, ColleagueID: b}, fromB.ID), domain.ErrNotInThread)
		if rows := w.colleagues(reader.ID, ""); len(rows) != 0 {
			t.Errorf("%s's Colleagues tab lists %+v, want nothing: they have no thread", who, rows)
		}
	}
	fromC := w.mustToColleague(by(c), a, "Tin của Chi cho An")
	thread, err := w.colleagueThread(c, a, domain.Cursor{})
	if err != nil || !slices.Equal(messageIDs(thread.Page.Messages), []string{fromC.ID}) {
		t.Errorf("C's thread with A after writing: %v, %v, want C's message alone", messageIDs(thread.Page.Messages), err)
	}
	thread, err = w.colleagueThread(b, a, domain.Cursor{})
	if err != nil || !slices.Equal(messageIDs(thread.Page.Messages), []string{fromA.ID, fromB.ID}) {
		t.Errorf("B's thread with A: %v, %v, want A's and B's messages and never C's", messageIDs(thread.Page.Messages), err)
	}
	var listed []string
	for _, row := range w.colleagues(a, "") {
		listed = append(listed, row.ColleagueID)
	}
	if !slices.Equal(listed, []string{c, b}) {
		t.Errorf("A's Colleagues tab lists %v, want C's thread then B's, newest first", listed)
	}
	if rows := w.colleagues(b, ""); len(rows) != 1 || rows[0].ColleagueID != a {
		t.Errorf("B's Colleagues tab lists %+v, want A's thread alone", rows)
	}
	if err := w.read(by(b), domain.ThreadRef{Kind: domain.KindColleague, ColleagueID: a}, fromC.ID); err == nil {
		t.Error("B moved their pointer to C's message to A")
	}
}

// TestTheColleaguesTabListsThreadsThenCandidates: the caller's threads,
// newest first, each with its colleague's role, a disabled colleague's
// flagged, the unread count past the caller's pointer and a preview; with a
// search, after them, at most MaxCandidates colleagues with no thread, by
// name, never a student, an account of the Admin console alone, a disabled
// or anonymised account, or the caller.
func TestTheColleaguesTabListsThreadsThenCandidates(t *testing.T) {
	w := newWorld(t)
	me := w.user("Cô Đào", "teacher")
	older, newer := w.user("Thầy Đức Cũ", "teacher"), w.user("Cô Đức Mới", "assistant")
	custom := w.holding("Đức Giáo vụ", w.role("Giáo vụ", "content.tests.write"))
	w.holding("Đức Kiểm toán", w.role("Kiểm toán", "system.audit.read"))
	w.user("Học viên Đức", "student")
	off := w.user("Đức Đã nghỉ", "teacher")
	w.disable(off)
	gone := w.user("Đức Ẩn danh", "teacher")
	w.anonymise(gone)

	w.mustToColleague(by(older), me, "Chào chị Đào.")
	second := w.mustToColleague(by(older), me, "Em gửi đề.")
	w.mustToColleague(by(me), older, "Cảm ơn em.")
	meeting := w.mustToColleague(by(newer), me, "Họp lúc 3 giờ nhé.")
	w.aged(meeting.ConversationID, time.Hour)
	if err := w.read(by(me), domain.ThreadRef{Kind: domain.KindColleague, ColleagueID: older}, second.ID); err != nil {
		t.Fatal(err)
	}
	w.mustToColleague(by(older), me, "Chị xem giúp em.")
	w.disable(newer)

	rows := w.colleagues(me, "")
	if len(rows) != 2 || rows[0].ColleagueID != older || rows[1].ColleagueID != newer {
		t.Fatalf("the tab lists %+v, want the thread written to last first, then the assistant's, an hour older", rows)
	}
	if r := rows[0]; r.Unread != 1 || r.Disabled || r.Candidate || r.Preview == nil || r.Preview.Text == nil || *r.Preview.Text != "Chị xem giúp em." ||
		r.Preview.Mine || r.RoleBuiltinKey == nil || *r.RoleBuiltinKey != "teacher" || r.RoleName == "" || r.LastMessageAt == nil ||
		r.AvatarURL == nil || !strings.HasPrefix(*r.AvatarURL, "https://photos.example/") {
		t.Errorf("the teacher's row: %+v, want one unread past the pointer, the newest message's preview, the role and a signed photo", r)
	}
	if r := rows[1]; r.Unread != 1 || !r.Disabled || r.RoleBuiltinKey == nil || *r.RoleBuiltinKey != "assistant" {
		t.Errorf("the disabled assistant's row: %+v, want it listed, disabled, one unread", r)
	}

	found := w.colleagues(me, "duc")
	var candidates []string
	for i, r := range found {
		if r.Candidate {
			candidates = append(candidates, r.ColleagueID)
			if r.LastMessageAt != nil || r.Preview != nil || r.Unread != 0 || r.Disabled {
				t.Errorf("a candidate carries a thread's state: %+v", r)
			}
		} else if i >= 2 || len(candidates) > 0 {
			t.Errorf("a thread is listed after a candidate: %+v", found)
		}
	}
	if !slices.Equal(candidates, []string{custom}) {
		t.Errorf("the search offers %v, want the custom staff role's account alone: never a thread's colleague again, a student, the Admin console alone, a disabled or anonymised account", candidates)
	}
	if got := w.colleagues(me, "moi"); len(got) != 1 || got[0].ColleagueID != newer {
		t.Errorf("an accent-free search of a thread's colleague: %+v", got)
	}

	for i := range domain.MaxCandidates + 3 {
		w.user(fmt.Sprintf("Đồng nghiệp số %02d", i), "teacher")
	}
	candidates = nil
	for _, r := range w.colleagues(me, "dong nghiep so") {
		if r.Candidate {
			candidates = append(candidates, r.ColleagueID)
		}
	}
	if len(candidates) != domain.MaxCandidates {
		t.Errorf("a search offers %d candidates, want at most %d", len(candidates), domain.MaxCandidates)
	}
}

// TestAColleagueMessageCountsAndRingsTheOtherMemberOnly runs the bell
// through the notifications module: a colleague's message counts in the
// reader's StaffUnread past their pointer, never the writer's own; it leaves
// the reader one message.received item per thread that leads to the
// colleague and names no class; reading clears it; a reader whose in-app
// switch is off counts the message and holds no item; a third staff member
// is never told.
func TestAColleagueMessageCountsAndRingsTheOtherMemberOnly(t *testing.T) {
	w := newWorld(t)
	notifications := notificationsapp.New(notificationsrepo.NewPostgres(db.NewContext(w.tx)))
	w.app.WithBell(adapters.MessagingBell{Notify: notifications.Commands.Notify, MarkKeyRead: notifications.Commands.MarkKeyRead}, nil)
	a, b, quiet, third := w.user("Cô An", "teacher"), w.user("Thầy Bảo", "teacher"), w.user("Cô Yên", "teacher"), w.user("Cô Ba", "teacher")
	w.exec(`INSERT INTO app.notification_preferences (user_id, event, in_app, email) VALUES ($1::uuid, 'message.received', false, true)`, quiet)

	w.mustToColleague(by(a), b, "Chị gửi em đề nghe.")
	newest := w.mustToColleague(by(a), b, "Em xem trước thứ Sáu nhé.")
	if n := w.staffUnread(by(b).Scope); n != 2 {
		t.Errorf("B's unread counts %d, want A's two messages", n)
	}
	if n := w.staffUnread(by(a).Scope); n != 0 {
		t.Errorf("A's unread counts %d, want 0: never their own", n)
	}
	got := w.items(b)
	if len(got) != 1 || got[0].kind != "message.received" || got[0].read || got[0].params["count"] != float64(2) ||
		got[0].params["senderName"] != "Cô An "+w.marker || got[0].params["className"] != nil ||
		got[0].target["route"] != "messages" || got[0].target["colleagueId"] != a || got[0].target["studentId"] != nil {
		t.Errorf("B holds %+v, want one message.received counting two, leading to A, naming no class", got)
	}
	var key string
	w.value(`SELECT dedupe_key FROM app.notifications WHERE user_id = $1::uuid AND kind = 'message.received'`, &key, b)
	if want := "msg:" + newest.ConversationID; key != want {
		t.Errorf("B's item is kept under %q, want R7's thread key %q, which anonymisation removes (T-F2.5)", key, want)
	}
	if got := w.items(a); len(got) != 0 {
		t.Errorf("A is told of their own messages: %+v", got)
	}
	if err := w.read(by(b), domain.ThreadRef{Kind: domain.KindColleague, ColleagueID: a}, newest.ID); err != nil {
		t.Fatal(err)
	}
	if n := w.staffUnread(by(b).Scope); n != 0 {
		t.Errorf("after reading, B counts %d", n)
	}
	if got := w.items(b); len(got) != 1 || !got[0].read {
		t.Errorf("after reading, B holds %+v, want the item read", got)
	}
	w.mustToColleague(by(b), a, "Dạ em cảm ơn chị.")
	if got := w.items(a); len(got) != 1 || got[0].target["colleagueId"] != b || got[0].params["count"] != float64(1) {
		t.Errorf("A holds %+v, want one item leading to B", got)
	}
	if n := w.staffUnread(by(a).Scope); n != 1 {
		t.Errorf("A counts %d after B's reply, want 1", n)
	}

	w.mustToColleague(by(a), quiet, "Chào chị Yên.")
	if n := w.staffUnread(by(quiet).Scope); n != 1 {
		t.Errorf("the reader whose in-app switch is off counts %d, want 1", n)
	}
	if got := w.items(quiet); len(got) != 0 {
		t.Errorf("the reader whose in-app switch is off holds %+v", got)
	}
	if got, n := w.items(third), w.staffUnread(by(third).Scope); len(got) != 0 || n != 0 {
		t.Errorf("a third staff member holds %+v and counts %d", got, n)
	}
}

// TestAColleagueThreadsFileIsForThePairAndItsUploaderBeforeSending: either
// colleague attaches a file as in a student's thread; the download admits
// the two of them once it is sent, and its uploader before; anyone else,
// another teacher, an Admin, a student, gets ErrAttachmentNotFound, as for a
// missing file.
func TestAColleagueThreadsFileIsForThePairAndItsUploaderBeforeSending(t *testing.T) {
	w := newWorld(t)
	w.withFiles()
	a, b, c, admin := w.user("Cô An", "teacher"), w.user("Thầy Bảo", "teacher"), w.user("Cô Chi", "teacher"), w.user("Quản trị", "admin")
	student := w.user("Học viên", "student")
	ctx := context.Background()
	staff := func(who actor.Actor, id string) error {
		_, err := w.app.Queries.StaffAttachmentURL.Handle(ctx, query.StaffAttachmentURL{ReaderID: who.ID, Scope: who.Scope, ID: id})
		return err
	}
	learner := func(who, id string) error {
		_, err := w.app.Queries.StudentAttachmentURL.Handle(ctx, query.StudentAttachmentURL{StudentID: who, ID: id})
		return err
	}
	send := func(from actor.Actor, to, file string) {
		t.Helper()
		if _, err := w.app.Commands.PostColleague.Handle(ctx, command.PostColleague{Actor: from, ColleagueID: to, ClientID: uuid.NewString(), AttachmentID: &file}); err != nil {
			t.Fatalf("sending %s: %v", file, err)
		}
	}

	file := w.mustUpload(a, "Đề nghe.pdf", fixture(t, "document.pdf"))
	if err := staff(by(a), file.ID); err != nil {
		t.Errorf("the uploader's unsent file: %v", err)
	}
	wantErr(t, "the unsent file for the colleague it is meant for", staff(by(b), file.ID), domain.ErrAttachmentNotFound)
	send(by(a), b, file.ID)
	reply := w.mustUpload(b, "Đáp án.pdf", fixture(t, "document.pdf"))
	send(by(b), a, reply.ID)
	for _, id := range []string{file.ID, reply.ID} {
		for name, err := range map[string]error{"A": staff(by(a), id), "B": staff(by(b), id)} {
			if err != nil {
				t.Errorf("a colleague thread's file %s for %s: %v", id, name, err)
			}
		}
		for name, err := range map[string]error{
			"another teacher":              staff(by(c), id),
			"an admin, whose scope is all": staff(byAdmin(admin), id),
			"a student":                    learner(student, id),
			"A on the student URL":         learner(a, id),
		} {
			wantErr(t, "a colleague thread's file for "+name, err, domain.ErrAttachmentNotFound)
		}
	}
	thread, err := w.colleagueThread(b, a, domain.Cursor{})
	if err != nil || len(thread.Page.Messages) != 2 || thread.Page.Messages[0].Attachment == nil || thread.Page.Messages[0].Attachment.ID != file.ID {
		t.Errorf("B's thread with A: %+v, %v, want A's file then B's", thread.Page.Messages, err)
	}
}

// TestThePickerListsColleaguesByNameWithTheirRole is listMessageRecipients
// for colleagues: every enabled account that opens the teacher workspace and
// is not a student, never the caller, by folded name, with the role's stored
// name and built-in key, no class names, and whether the caller's thread
// exists; paged by a cursor. Every staff member gets the same rows. A
// students' filter and a cursor the picker did not give are refused.
func TestThePickerListsColleaguesByNameWithTheirRole(t *testing.T) {
	w := newWorld(t)
	me, admin := w.user("Ông Chủ", "teacher"), w.user("Bà Quản trị", "admin")
	teacher := w.user("Anh Giáo viên", "teacher")
	custom := w.holding("Chị Giáo vụ", w.role("Giáo vụ", "people.students.read"))
	assistant := w.user("Em Trợ giảng", "assistant")
	w.holding("Cô Kiểm toán", w.role("Kiểm toán", "system.audit.read"))
	w.user("Dũng Học viên", "student")
	off := w.user("Dì Đã nghỉ", "teacher")
	w.disable(off)
	w.mustToColleague(by(me), teacher, "Chào anh.")

	page, err := w.colleagueRecipients(by(me), domain.RecipientFilter{Query: w.marker}, "")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{teacher, admin, custom, assistant}; !slices.Equal(ids(page.Rows), want) || page.Next != "" {
		t.Fatalf("the picker lists %v (next %q), want %v by name", ids(page.Rows), page.Next, want)
	}
	for i, c := range []struct {
		builtin      string
		threadExists bool
	}{{"teacher", true}, {"admin", false}, {"", false}, {"assistant", false}} {
		row := page.Rows[i]
		if row.RoleName == nil || *row.RoleName == "" || (c.builtin == "") != (row.RoleBuiltinKey == nil) ||
			row.RoleBuiltinKey != nil && *row.RoleBuiltinKey != c.builtin || row.ThreadExists != c.threadExists || !row.CanPost ||
			len(row.ClassNames) != 0 || len(row.OtherClassNames) != 0 || row.ClassNames == nil || row.OtherClassNames == nil {
			t.Errorf("row %d: %+v, want role %q, thread %v, canPost, and empty class names", i, row, c.builtin, c.threadExists)
		}
	}
	if custom := page.Rows[2]; *custom.RoleName != "Giáo vụ "+w.marker {
		t.Errorf("the custom role's name: %q", *custom.RoleName)
	}
	if adminView, err := w.colleagueRecipients(byAdmin(admin), domain.RecipientFilter{Query: w.marker}, ""); err != nil ||
		!slices.Equal(ids(adminView.Rows), []string{teacher, custom, assistant, me}) {
		t.Errorf("the Admin's picker: %v, %v, want the same colleagues but the Admin, and the caller of the first in their place", ids(adminView.Rows), err)
	}

	var got []string
	cursor := ""
	for range 4 {
		rows, err := w.colleagueRecipients(by(me), domain.RecipientFilter{Query: w.marker, Limit: 1}, cursor)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, ids(rows.Rows)...)
		if cursor = rows.Next; cursor == "" {
			break
		}
	}
	if !slices.Equal(got, ids(page.Rows)) || cursor != "" {
		t.Errorf("paged one at a time: %v (next %q), want %v", got, cursor, ids(page.Rows))
	}

	for name, f := range map[string]domain.RecipientFilter{
		"a class":     {ClassID: uuid.NewString()},
		"mine":        {Mine: true},
		"in no class": {InNoClass: true},
	} {
		_, err := w.colleagueRecipients(by(me), f, "")
		wantErr(t, "colleagues with "+name, err, domain.ErrInvalid)
	}
	for name, cursor := range map[string]string{
		"not base64":            "%%%",
		"another list's cursor": base64.RawURLEncoding.EncodeToString([]byte("threads:" + teacher)),
		"no uuid":               base64.RawURLEncoding.EncodeToString([]byte("recipients:" + "an-id")),
	} {
		_, err := w.colleagueRecipients(by(me), domain.RecipientFilter{Query: w.marker}, cursor)
		wantErr(t, "a colleagues cursor the picker did not give: "+name, err, domain.ErrInvalid)
	}
}
