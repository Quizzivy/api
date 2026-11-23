//go:build integration

package application_test

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"quizzivy/internal/modules/messaging/application/command"
	"quizzivy/internal/modules/messaging/domain"
)

func TestARepeatedClientIDReturnsTheMessageItMade(t *testing.T) {
	w := newWorld(t)
	teacher := w.user("Cô Mai", "teacher")
	mai := by(teacher)
	class := w.class(teacher, "Lớp 8A")
	student, other := w.user("Học viên Hà", "student"), w.user("Học viên Hùng", "student")
	w.enrol(class, student)
	w.enrol(class, other)
	ctx := context.Background()

	clientID := uuid.NewString()
	post := command.PostDirect{Actor: mai, StudentID: student, ClientID: clientID, Body: text("Mai kiểm tra 15 phút.")}
	first, err := w.app.Commands.PostDirect.Handle(ctx, post)
	if err != nil || !first.Created {
		t.Fatalf("first send: %+v, %v", first, err)
	}
	post.Body = text("Một nội dung khác khi gửi lại")
	again, err := w.app.Commands.PostDirect.Handle(ctx, post)
	if err != nil || again.Created || again.Message.ID != first.Message.ID || *again.Message.Body != "Mai kiểm tra 15 phút." {
		t.Errorf("the retry: %+v, %v, want the first message, not created", again, err)
	}
	var count int
	w.value(`SELECT count(*) FROM app.messages WHERE sender_id = $1::uuid`, &count, teacher)
	if count != 1 {
		t.Errorf("the teacher has %d messages after a retry, want 1", count)
	}
	w.disable(student)
	if replay, err := w.app.Commands.PostDirect.Handle(ctx, post); err != nil || replay.Message.ID != first.Message.ID {
		t.Errorf("a retry after the account was disabled: %+v, %v, want the message the first call made", replay, err)
	}
	_, err = w.app.Commands.PostDirect.Handle(ctx, command.PostDirect{Actor: mai, StudentID: other, ClientID: clientID, Body: text("Chào em")})
	wantErr(t, "the clientId reused in another thread", err, domain.ErrClientIDTaken)

	announcement := command.PostAnnouncement{Actor: mai, MayWrite: true, ClassID: class, ClientID: uuid.NewString(), Body: text("Nghỉ học thứ Sáu.")}
	made, err := w.app.Commands.PostAnnouncement.Handle(ctx, announcement)
	if err != nil || !made.Created {
		t.Fatalf("the announcement: %+v, %v", made, err)
	}
	if again, err := w.app.Commands.PostAnnouncement.Handle(ctx, announcement); err != nil || again.Created || again.Message.ID != made.Message.ID {
		t.Errorf("the announcement's retry: %+v, %v", again, err)
	}
	w.archive(class)
	if again, err := w.app.Commands.PostAnnouncement.Handle(ctx, announcement); err != nil || again.Message.ID != made.Message.ID {
		t.Errorf("the announcement's retry after the class was archived: %+v, %v, want the message it made", again, err)
	}
}

func TestAPostValidatesItsBodyAndRefusesAFileThatIsNotTheSenders(t *testing.T) {
	w := newWorld(t)
	teacher := w.user("Cô Yến", "teacher")
	yen := by(teacher)
	class := w.class(teacher, "Lớp 7B")
	student := w.user("Học viên Vy", "student")
	w.enrol(class, student)
	ctx := context.Background()
	file := uuid.NewString()

	_, err := w.app.Commands.PostDirect.Handle(ctx, command.PostDirect{Actor: yen, StudentID: student, ClientID: uuid.NewString(), Body: text(" \n\t ")})
	wantErr(t, "a blank body", err, domain.ErrEmpty)
	_, err = w.app.Commands.PostDirect.Handle(ctx, command.PostDirect{Actor: yen, StudentID: student, ClientID: uuid.NewString(), AttachmentID: &file})
	wantErr(t, "a file alone", err, domain.ErrAttachmentNotFound)
	_, err = w.app.Commands.PostAnnouncement.Handle(ctx, command.PostAnnouncement{Actor: yen, MayWrite: true, ClassID: class, ClientID: uuid.NewString(), Body: text("Xem tệp"), AttachmentID: &file})
	wantErr(t, "an announcement with a file", err, domain.ErrAttachmentNotFound)
	_, err = w.app.Commands.PostDirect.Handle(ctx, command.PostDirect{Actor: yen, StudentID: uuid.NewString(), ClientID: uuid.NewString(), AttachmentID: &file})
	wantErr(t, "a file to a missing student", err, domain.ErrNotFound)

	posted, err := w.direct(yen, student, "  Chào em \n")
	if err != nil || *posted.Message.Body != "Chào em" {
		t.Errorf("a padded body: %+v, %v, want it stored trimmed", posted, err)
	}
	var threads int
	w.value(`SELECT count(*) FROM app.conversations WHERE staff_id = $1::uuid OR class_id = $2::uuid`, &threads, teacher, class)
	if threads != 1 {
		t.Errorf("%d threads after refused posts and one sent, want 1: a refused post makes no thread", threads)
	}
}

func TestBeforeAndAfterPageAThread(t *testing.T) {
	w := newWorld(t)
	teacher := w.user("Cô Ngọc", "teacher")
	ngoc := by(teacher)
	class := w.class(teacher, "Lớp 6A")
	student := w.user("Học viên Phúc", "student")
	w.enrol(class, student)
	var ids []string
	for i := range 7 {
		ids = append(ids, w.mustDirect(ngoc, student, "Tin nhắn "+string(rune('A'+i))).ID)
	}
	page := func(c domain.Cursor) domain.Page {
		t.Helper()
		thread, err := w.thread(teacher, student, c)
		if err != nil {
			t.Fatal(err)
		}
		return thread.Page
	}
	idsOf := func(p domain.Page) []string {
		var out []string
		for _, m := range p.Messages {
			out = append(out, m.ID)
		}
		return out
	}

	newest := page(domain.Cursor{Limit: 3})
	if !slices.Equal(idsOf(newest), ids[4:]) || newest.NextBefore == nil || *newest.NextBefore != ids[4] || *newest.LatestID != ids[6] {
		t.Fatalf("the newest page = %v next %v latest %v, want %v oldest first", idsOf(newest), newest.NextBefore, newest.LatestID, ids[4:])
	}
	older := page(domain.Cursor{Before: *newest.NextBefore, Limit: 3})
	if !slices.Equal(idsOf(older), ids[1:4]) || older.NextBefore == nil || *older.NextBefore != ids[1] {
		t.Errorf("the page before = %v next %v, want %v", idsOf(older), older.NextBefore, ids[1:4])
	}
	oldest := page(domain.Cursor{Before: *older.NextBefore, Limit: 3})
	if !slices.Equal(idsOf(oldest), ids[:1]) || oldest.NextBefore != nil || *oldest.LatestID != ids[6] {
		t.Errorf("the oldest page = %v next %v latest %v, want only the first and no next", idsOf(oldest), oldest.NextBefore, oldest.LatestID)
	}
	poll := page(domain.Cursor{After: ids[2], Limit: 3})
	if !slices.Equal(idsOf(poll), ids[3:6]) || poll.NextBefore != nil {
		t.Errorf("the poll after the third = %v next %v, want the three after it, oldest first", idsOf(poll), poll.NextBefore)
	}
	if caught := page(domain.Cursor{After: ids[6]}); len(caught.Messages) != 0 || *caught.LatestID != ids[6] {
		t.Errorf("a poll after the newest = %v, want none and the same latest", idsOf(caught))
	}
	if all := page(domain.Cursor{}); !slices.Equal(idsOf(all), ids) || all.NextBefore != nil {
		t.Errorf("the default page = %v next %v, want every message", idsOf(all), all.NextBefore)
	}
	if _, err := w.thread(teacher, student, domain.Cursor{Before: ids[3], After: ids[1]}); err == nil {
		t.Error("before and after together were accepted")
	}
	empty, err := w.thread(teacher, w.user("Học viên chưa nhắn", "student", teacher), domain.Cursor{})
	if err != nil || len(empty.Page.Messages) != 0 || empty.Page.LatestID != nil || empty.Page.NextBefore != nil {
		t.Errorf("a thread before its first post: %+v, %v, want empty", empty, err)
	}
}

func TestSeenByCountsTheActiveStudentsWhosePointerReachedThePost(t *testing.T) {
	w := newWorld(t)
	teacher := w.user("Cô Linh", "teacher")
	linh := by(teacher)
	class := w.class(teacher, "Lớp 12A")
	a, b, c := w.user("Học viên A", "student"), w.user("Học viên B", "student"), w.user("Học viên C", "student")
	gone := w.user("Học viên bị khoá", "student")
	for _, s := range []string{a, b, c, gone} {
		w.enrol(class, s)
	}
	first := w.mustAnnounce(linh, class, "Thông báo 1")
	second := w.mustAnnounce(linh, class, "Thông báo 2")
	third := w.mustAnnounce(linh, class, "Thông báo 3")

	pointer := func(user, message string) {
		w.exec(`INSERT INTO app.conversation_reads (conversation_id, user_id, last_read_message_id)
			SELECT conversation_id, $1::uuid, id FROM app.messages WHERE id = $2::uuid
			ON CONFLICT (conversation_id, user_id) DO UPDATE SET last_read_message_id = EXCLUDED.last_read_message_id`, user, message)
	}
	pointer(a, third.ID)
	pointer(b, second.ID)
	pointer(gone, third.ID)
	w.disable(gone)

	thread, err := w.channel(linh, class, domain.Cursor{})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]domain.SeenBy{first.ID: {Seen: 2, Of: 3}, second.ID: {Seen: 2, Of: 3}, third.ID: {Seen: 1, Of: 3}}
	for _, m := range thread.Page.Messages {
		if m.SeenBy == nil || *m.SeenBy != want[m.ID] {
			t.Errorf("post %s is seen by %+v, want %+v: of the three active students, those whose pointer reached it", m.ID, m.SeenBy, want[m.ID])
		}
	}
	if thread.Channel.StudentCount != 3 {
		t.Errorf("the channel counts %d students, want the 3 active ones", thread.Channel.StudentCount)
	}
	w.remove(class, b)
	thread, _ = w.channel(linh, class, domain.Cursor{})
	if m := thread.Page.Messages[0]; *m.SeenBy != (domain.SeenBy{Seen: 1, Of: 2}) {
		t.Errorf("after one student left, the first post is seen by %+v, want 1 of the 2 members now", m.SeenBy)
	}
	direct := w.mustDirect(linh, c, "Chào em")
	dm, _ := w.thread(teacher, c, domain.Cursor{})
	if dm.Page.Messages[0].SeenBy != nil || direct.SeenBy != nil {
		t.Error("a direct message carries seenBy")
	}
}

func TestAnArchivedClassKeepsItsChannelReadableAndTakesNoPost(t *testing.T) {
	w := newWorld(t)
	teacher := w.user("Cô Trang", "teacher")
	trang := by(teacher)
	class := w.class(teacher, "Lớp đã lưu trữ")
	posted := w.mustAnnounce(trang, class, "Chào cả lớp")
	w.archive(class)

	_, err := w.announce(trang, class, "Thêm một thông báo")
	wantErr(t, "an announcement to an archived class", err, domain.ErrClassArchived)
	thread, err := w.channel(trang, class, domain.Cursor{})
	if err != nil || !thread.Channel.Archived || len(thread.Page.Messages) != 1 || thread.Page.Messages[0].ID != posted.ID {
		t.Errorf("the archived channel: %+v, %v, want it readable with its one post", thread, err)
	}
	for _, th := range w.mine(w.list(trang, domain.TabAll, "")) {
		if th.Ref.ClassID == class {
			t.Errorf("the archived class's channel is listed: %+v", th)
		}
	}
	if err := w.read(trang, domain.ThreadRef{Kind: domain.KindClass, ClassID: class}, posted.ID); err != nil {
		t.Errorf("marking the archived channel read: %v", err)
	}
	_, err = w.app.Commands.PostAnnouncement.Handle(context.Background(), command.PostAnnouncement{Actor: trang, ClassID: w.class(teacher, "Lớp mở"), ClientID: uuid.NewString(), Body: text("x")})
	wantErr(t, "an announcement without teaching.classes.write", err, domain.ErrForbidden)
}

func TestTheListShowsEveryChannelInScopePostedOrNot(t *testing.T) {
	w := newWorld(t)
	teacher, other, admin := w.user("Cô Hương", "teacher"), w.user("Thầy Khoa", "teacher"), w.user("Quản trị", "admin")
	huong, khoa := by(teacher), by(other)
	quiet, busy := w.class(teacher, "Lớp B chưa đăng"), w.class(teacher, "Lớp C có đăng")
	w.class(teacher, "Lớp A chưa đăng")
	theirs := w.class(other, "Lớp của thầy Khoa")
	student, kept := w.user("Học viên Quang", "student"), w.user("Học viên Sơn", "student")
	w.enrol(busy, student)
	w.enrol(busy, kept)
	w.enrol(quiet, kept)
	w.mustAnnounce(huong, busy, "Chào lớp C")
	w.mustDirect(huong, kept, "Chào Sơn")
	// One transaction has one now(): the channel's post is made the older.
	w.exec(`UPDATE app.conversations SET last_message_at = last_message_at - interval '1 hour' WHERE class_id = $1::uuid`, busy)
	w.mustAnnounce(khoa, theirs, "Chào lớp của thầy")

	rows := w.mine(w.list(huong, domain.TabAll, ""))
	var titles []string
	for _, r := range rows {
		titles = append(titles, strings.TrimSuffix(r.Title, " "+w.marker))
	}
	if !slices.Equal(titles, []string{"Học viên Sơn", "Lớp C có đăng", "Lớp A chưa đăng", "Lớp B chưa đăng"}) {
		t.Fatalf("the list = %v, want the newest activity first and the channels nobody posted in last, by name", titles)
	}
	quietRow := rows[3]
	if quietRow.Ref != (domain.ThreadRef{Kind: domain.KindClass, ClassID: quiet}) || quietRow.LastMessageAt != nil || quietRow.Preview != nil ||
		quietRow.Unread != 0 || quietRow.StudentCount == nil || *quietRow.StudentCount != 1 || quietRow.AvatarURL != nil {
		t.Errorf("a channel with no post: %+v", quietRow)
	}
	if busyRow := rows[1]; busyRow.Preview == nil || !busyRow.Preview.Mine || *busyRow.Preview.Text != "Chào lớp C" || *busyRow.StudentCount != 2 {
		t.Errorf("the posted channel: %+v", busyRow)
	}
	if dm := rows[0]; dm.Ref.StudentID != kept || dm.StudentCount != nil || dm.AvatarURL == nil || !strings.HasPrefix(*dm.AvatarURL, "https://photos.example/") ||
		!slices.Equal(dm.ClassNames, []string{"Lớp B chưa đăng " + w.marker, "Lớp C có đăng " + w.marker}) {
		t.Errorf("the direct row: %+v", dm)
	}
	for _, r := range rows {
		if r.Ref.ClassID == theirs {
			t.Errorf("another teacher's channel is listed: %+v", r)
		}
	}
	if classes := w.mine(w.list(huong, domain.TabClasses, "")); len(classes) != 3 {
		t.Errorf("the classes tab = %d rows, want the 3 channels", len(classes))
	}
	if directs := w.mine(w.list(huong, domain.TabDirect, "")); len(directs) != 1 || directs[0].Ref.Kind != domain.KindDirect {
		t.Errorf("the direct tab = %+v", directs)
	}
	found := w.mine(w.list(huong, domain.TabAll, "lop c"))
	if len(found) != 3 || found[0].Ref.StudentID != kept || found[1].Ref.ClassID != busy || found[2].Ref.StudentID != student || !found[2].Candidate {
		t.Errorf("a search on a class name = %+v, want the thread of a student in it, its channel, and its other student to write to", found)
	}

	all := w.mine(w.list(byAdmin(admin), domain.TabAll, ""))
	channels := 0
	for _, r := range all {
		if r.Ref.Kind == domain.KindDirect {
			t.Errorf("scope.all lists a direct thread: %+v", r)
		}
		channels++
	}
	if channels != 4 {
		t.Errorf("scope.all lists %d channels, want every class's 4", channels)
	}
	if _, err := w.channel(byAdmin(admin), busy, domain.Cursor{}); err != nil {
		t.Errorf("scope.all reading a class channel: %v", err)
	}
	_, err := w.channel(khoa, busy, domain.Cursor{})
	wantErr(t, "another teacher reading the channel", err, domain.ErrNotFound)
}

func TestUnreadCountsOthersMessagesPastThePointer(t *testing.T) {
	w := newWorld(t)
	teacher := w.user("Cô Uyên", "teacher")
	uyen := by(teacher)
	class := w.class(teacher, "Lớp 5A")
	student, outsider := w.user("Học viên Bảo", "student"), w.user("Học viên khác", "student")
	w.enrol(class, student)
	mine := w.mustDirect(uyen, student, "Em làm bài chưa?")
	one := w.studentWrites(teacher, student, "Dạ rồi ạ.")
	two := w.studentWrites(teacher, student, "Em gửi cô.")

	unread := func() int {
		for _, r := range w.mine(w.list(uyen, domain.TabDirect, "")) {
			if r.Ref.StudentID == student {
				if r.Preview == nil || r.Preview.Mine || *r.Preview.Text != "Em gửi cô." {
					t.Errorf("the preview = %+v, want the student's newest message", r.Preview)
				}
				return r.Unread
			}
		}
		t.Fatal("the thread is not listed")
		return -1
	}
	if n := unread(); n != 2 {
		t.Errorf("unread = %d, want the student's two, not the teacher's own", n)
	}
	ref := domain.ThreadRef{Kind: domain.KindDirect, StudentID: student}
	if err := w.read(uyen, ref, one); err != nil {
		t.Fatal(err)
	}
	if n := unread(); n != 1 {
		t.Errorf("unread after reading the first = %d, want 1", n)
	}
	if err := w.read(uyen, ref, two); err != nil {
		t.Fatal(err)
	}
	if err := w.read(uyen, ref, mine.ID); err != nil {
		t.Errorf("an older pointer: %v, want a no-op", err)
	}
	if n := unread(); n != 0 {
		t.Errorf("unread after an older pointer = %d, want 0: the pointer only moves forward", n)
	}
	w.enrol(class, outsider)
	elsewhere := w.mustDirect(uyen, outsider, "Chào em")
	wantErr(t, "a pointer to another thread's message", w.read(uyen, ref, elsewhere.ID), domain.ErrNotInThread)
	wantErr(t, "a pointer to no message", w.read(uyen, ref, uuid.NewString()), domain.ErrNotInThread)
	wantErr(t, "a pointer in a thread before its first post", w.read(uyen, domain.ThreadRef{Kind: domain.KindDirect, StudentID: w.user("Học viên mới", "student", teacher)}, mine.ID), domain.ErrNotInThread)
	wantErr(t, "another teacher's pointer naming this thread's message", w.read(by(w.user("Thầy khác", "teacher")), ref, two), domain.ErrNotInThread)
	announced := w.mustAnnounce(uyen, class, "Chào lớp")
	wantErr(t, "a class message named in a direct thread", w.read(uyen, ref, announced.ID), domain.ErrNotInThread)
	wantErr(t, "a direct message named in the channel", w.read(uyen, domain.ThreadRef{Kind: domain.KindClass, ClassID: class}, two), domain.ErrNotInThread)

	var at time.Time
	w.value(`SELECT last_message_at FROM app.conversations WHERE kind = 'class' AND class_id = $1::uuid`, &at, class)
	if !at.Equal(announced.CreatedAt) {
		t.Errorf("the channel's last_message_at = %v, want its post's %v", at, announced.CreatedAt)
	}
	var audited int
	w.value(`SELECT count(*) FROM app.audit_log WHERE actor_user_id = $1::uuid`, &audited, teacher)
	if audited != 0 {
		t.Errorf("posts and read pointers wrote %d audit rows, want none: the messages table is the record (D17)", audited)
	}
}
