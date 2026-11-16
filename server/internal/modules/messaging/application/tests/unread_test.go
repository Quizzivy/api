//go:build integration

package application_test

import (
	"context"
	"encoding/json"
	"slices"
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
	"quizzivy/internal/shared/access"
)

func (w *world) staffUnread(scope access.Scope) int {
	w.t.Helper()
	n, err := w.app.Queries.StaffUnread.Handle(context.Background(), query.StaffUnread{Scope: scope})
	if err != nil {
		w.t.Fatal(err)
	}
	return n
}

func (w *world) studentUnread(student string) int {
	w.t.Helper()
	n, err := w.app.Queries.StudentUnread.Handle(context.Background(), query.StudentUnread{StudentID: student})
	if err != nil {
		w.t.Fatal(err)
	}
	return n
}

// TestTheUnreadCountsSumEveryThreadPastThePointerButTheReadersOwn fails when
// a count leaves out a thread, counts the reader's own messages, ignores the
// pointer, or counts an archived class, a class out of scope or a class the
// student left.
func TestTheUnreadCountsSumEveryThreadPastThePointerButTheReadersOwn(t *testing.T) {
	w := newWorld(t)
	teacher, colleague, admin := w.user("Cô Uyên", "teacher"), w.user("Thầy Khoa", "teacher"), w.user("Cô Quản trị", "admin")
	uyen, khoa := by(teacher), by(colleague)
	class, other := w.class(teacher, "Lớp 5A"), w.class(colleague, "Lớp 6B")
	an, binh := w.user("Học viên An", "student"), w.user("Học viên Bình", "student")
	w.enrol(class, an)
	w.enrol(class, binh)
	w.enrol(other, an)

	if n := w.staffUnread(uyen.Scope); n != 0 {
		t.Fatalf("a teacher with no message has %d unread", n)
	}
	w.mustDirect(uyen, an, "Em làm bài chưa?")
	first := w.studentWrites(teacher, an, "Dạ rồi ạ.")
	w.studentWrites(teacher, an, "Em gửi cô.")
	w.studentWrites(teacher, binh, "Cô ơi")
	announced := w.mustAnnounce(uyen, class, "Chào lớp")
	w.mustAnnounce(khoa, other, "Chào lớp 6B")
	w.mustAnnounce(byAdmin(admin), class, "Nhà trường thông báo")

	if n := w.staffUnread(uyen.Scope); n != 4 {
		t.Errorf("the teacher has %d unread, want 4: three students' messages and the admin's post, not her own", n)
	}
	if n := w.staffUnread(khoa.Scope); n != 0 {
		t.Errorf("the colleague has %d unread, want 0: his own post, and no thread with the teacher's class", n)
	}
	if err := w.read(uyen, domain.ThreadRef{Kind: domain.KindDirect, StudentID: an}, first); err != nil {
		t.Fatal(err)
	}
	if n := w.staffUnread(uyen.Scope); n != 3 {
		t.Errorf("after reading An's first message the teacher has %d unread, want 3", n)
	}

	if n := w.studentUnread(an); n != 4 {
		t.Errorf("An has %d unread, want 4: the direct message and the three posts, not her own", n)
	}
	if err := w.studentRead(an, command.MarkReadAsStudent{ClassID: class, MessageID: announced.ID}); err != nil {
		t.Fatal(err)
	}
	if n := w.studentUnread(an); n != 3 {
		t.Errorf("after reading the teacher's post An has %d unread, want 3", n)
	}
	if n := w.studentUnread(binh); n != 2 {
		t.Errorf("Bình has %d unread, want 2: the class's posts, not his own message", n)
	}

	w.archive(other)
	if n := w.studentUnread(an); n != 2 {
		t.Errorf("with 6B archived An has %d unread, want 2", n)
	}
	w.remove(class, binh)
	if n := w.studentUnread(binh); n != 0 {
		t.Errorf("Bình left the class and has %d unread, want 0", n)
	}
	w.archive(class)
	if n := w.staffUnread(uyen.Scope); n != 2 {
		t.Errorf("with her class archived the teacher has %d unread, want 2, her direct threads", n)
	}
	if n := w.studentUnread(teacher); n != 0 {
		t.Errorf("a staff member's student count is %d, want 0", n)
	}
}

type bell struct {
	rung []domain.Notice
	seen []string
}

func (b *bell) Ring(_ context.Context, n domain.Notice) error {
	b.rung = append(b.rung, n)
	return nil
}

func (b *bell) Seen(_ context.Context, readerID, conversationID string, _ time.Time) error {
	b.seen = append(b.seen, readerID+" "+conversationID)
	return nil
}

func (b *bell) readers() []string {
	var out []string
	for _, n := range b.rung {
		out = append(out, n.ReaderID)
	}
	slices.Sort(out)
	return out
}

// TestANewMessageRingsTheOtherSideOrTheClassesActiveStudents fails when a
// post rings its sender, a disabled student, someone who left the class or
// another class's student, rings anyone on a replay, or clears an item while
// something in the thread is still unread.
func TestANewMessageRingsTheOtherSideOrTheClassesActiveStudents(t *testing.T) {
	w := newWorld(t)
	rang := &bell{}
	w.app.WithBell(rang, nil)
	teacher := w.user("Cô Uyên", "teacher")
	uyen := by(teacher)
	class := w.class(teacher, "Lớp 5A")
	an, binh, chi, dung := w.user("Học viên An", "student"), w.user("Học viên Bình", "student"), w.user("Học viên Chi", "student"), w.user("Học viên Dũng", "student")
	for _, s := range []string{an, binh, chi, dung} {
		w.enrol(class, s)
	}
	w.enrol(w.class(w.user("Thầy Khoa", "teacher"), "Lớp 6B"), w.user("Học viên lớp khác", "student"))
	w.disable(chi)
	w.remove(class, dung)

	clientID := uuid.NewString()
	posted, err := w.app.Commands.PostDirect.Handle(context.Background(), command.PostDirect{
		Actor: uyen, StudentID: an, ClientID: clientID, Body: text("Em làm bài chưa?"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := rang.rung; len(got) != 1 || got[0].ReaderID != an || !got[0].ToStudent || got[0].TeacherID != teacher ||
		got[0].ConversationID != posted.Message.ConversationID || got[0].Excerpt != "Em làm bài chưa?" || got[0].SenderName != "Cô Uyên "+w.marker {
		t.Errorf("the direct message rang %+v, want An told of the teacher's thread", got)
	}
	rang.rung = nil
	if _, err := w.app.Commands.PostDirect.Handle(context.Background(), command.PostDirect{
		Actor: uyen, StudentID: an, ClientID: clientID, Body: text("Em làm bài chưa?"),
	}); err != nil {
		t.Fatal(err)
	}
	if len(rang.rung) != 0 {
		t.Errorf("a replayed clientId rang %+v, want nobody", rang.rung)
	}

	reply := w.studentWrites(teacher, an, "Dạ rồi ạ.")
	if got := rang.rung; len(got) != 1 || got[0].ReaderID != teacher || got[0].ToStudent || got[0].StudentID != an {
		t.Errorf("the student's reply rang %+v, want the teacher told of An's thread", got)
	}
	rang.rung = nil
	w.mustAnnounce(uyen, class, "Chào lớp")
	want := []string{an, binh}
	slices.Sort(want)
	if got := rang.readers(); !slices.Equal(got, want) {
		t.Errorf("the announcement rang %v, want the class's two active students %v", got, want)
	}
	for _, n := range rang.rung {
		if !n.ToStudent || n.ClassID != class || n.ClassName == nil || *n.ClassName != "Lớp 5A "+w.marker {
			t.Errorf("the announcement told %+v, want the class's channel and name", n)
		}
	}

	ref := domain.ThreadRef{Kind: domain.KindDirect, StudentID: an}
	another := w.studentWrites(teacher, an, "Em gửi cô.")
	if err := w.read(uyen, ref, reply); err != nil {
		t.Fatal(err)
	}
	if len(rang.seen) != 0 {
		t.Errorf("a pointer short of the newest message cleared %v", rang.seen)
	}
	if err := w.read(uyen, ref, another); err != nil {
		t.Fatal(err)
	}
	if want := teacher + " " + posted.Message.ConversationID; !slices.Equal(rang.seen, []string{want}) {
		t.Errorf("the pointer at the newest message cleared %v, want %v", rang.seen, []string{want})
	}
}

// TestADeliveryLeavesOutAReaderWhosePointerReachedTheMessage fails when a
// reader who already read the message is rung for it.
func TestADeliveryLeavesOutAReaderWhosePointerReachedTheMessage(t *testing.T) {
	w := newWorld(t)
	teacher := w.user("Cô Uyên", "teacher")
	class := w.class(teacher, "Lớp 5A")
	an, binh := w.user("Học viên An", "student"), w.user("Học viên Bình", "student")
	w.enrol(class, an)
	w.enrol(class, binh)
	post := w.mustAnnounce(by(teacher), class, "Chào lớp")
	if err := w.studentRead(an, command.MarkReadAsStudent{ClassID: class, MessageID: post.ID}); err != nil {
		t.Fatal(err)
	}
	d, err := messagingrepo.NewPostgres(db.NewContext(w.tx)).Delivery(context.Background(), post.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(d.Readers, []string{binh}) || d.ClassName != "Lớp 5A "+w.marker || d.Kind != domain.KindClass {
		t.Errorf("the delivery is %+v, want Bình alone, who has not read it", d)
	}
}

type item struct {
	id     string
	kind   string
	params map[string]any
	target map[string]any
	read   bool
}

func (w *world) items(user string) []item {
	w.t.Helper()
	rows, err := w.tx.Query(context.Background(), `SELECT id::text, kind, params, target, read_at IS NOT NULL
		FROM app.notifications WHERE user_id = $1::uuid ORDER BY id DESC`, user)
	if err != nil {
		w.t.Fatal(err)
	}
	defer rows.Close()
	var out []item
	for rows.Next() {
		var it item
		var params, target []byte
		if err := rows.Scan(&it.id, &it.kind, &params, &target, &it.read); err != nil {
			w.t.Fatal(err)
		}
		if err := json.Unmarshal(params, &it.params); err != nil {
			w.t.Fatal(err)
		}
		if err := json.Unmarshal(target, &it.target); err != nil {
			w.t.Fatal(err)
		}
		out = append(out, it)
	}
	return out
}

// TestAThreadsItemCountsItsMessagesUntilThePointerPassesIt runs the bell
// through the notifications module on the same transaction. It fails when a
// thread leaves more than one item per reader, the count does not follow the
// unread messages or does not start again once read, the item is not cleared
// when the pointer passes it, or a reader who switched message.received off
// is told.
func TestAThreadsItemCountsItsMessagesUntilThePointerPassesIt(t *testing.T) {
	w := newWorld(t)
	notifications := notificationsapp.New(notificationsrepo.NewPostgres(db.NewContext(w.tx)))
	w.app.WithBell(adapters.MessagingBell{Notify: notifications.Commands.Notify, MarkKeyRead: notifications.Commands.MarkKeyRead}, nil)
	teacher := w.user("Cô Uyên", "teacher")
	uyen := by(teacher)
	class := w.class(teacher, "Lớp 5A")
	an, binh := w.user("Học viên An", "student"), w.user("Học viên Bình", "student")
	w.enrol(class, an)
	w.enrol(class, binh)
	w.exec(`INSERT INTO app.notification_preferences (user_id, event, in_app, email) VALUES ($1::uuid, 'message.received', false, false)`, binh)

	w.mustDirect(uyen, an, "Em làm bài chưa?")
	second := w.mustDirect(uyen, an, "Hạn nộp là thứ Sáu.")
	third := w.mustDirect(uyen, an, "Nhớ ghi tên vào bài nhé, em.")
	got := w.items(an)
	if len(got) != 1 {
		t.Fatalf("three messages left An %d items, want one for the thread: %+v", len(got), got)
	}
	first := got[0]
	if first.kind != "message.from_teacher" || first.read || first.params["count"] != float64(3) ||
		first.params["excerpt"] != "Nhớ ghi tên vào bài nhé, em." || first.params["senderName"] != "Cô Uyên "+w.marker ||
		first.params["className"] != nil || first.target["route"] != "studentMessages" || first.target["teacherId"] != teacher {
		t.Errorf("An's item is %+v, want an unread message.from_teacher counting three, quoting the newest", first)
	}

	ref := command.MarkReadAsStudent{TeacherID: teacher, MessageID: second.ID}
	if err := w.studentRead(an, ref); err != nil {
		t.Fatal(err)
	}
	if got := w.items(an); got[0].read {
		t.Error("a pointer short of the newest message marked the item read")
	}
	ref.MessageID = third.ID
	if err := w.studentRead(an, ref); err != nil {
		t.Fatal(err)
	}
	if got := w.items(an); len(got) != 1 || !got[0].read || got[0].id != first.id {
		t.Errorf("after reading the newest message An holds %+v, want the same item, read", got)
	}

	w.mustDirect(uyen, an, "Cô đã chấm bài.")
	again := w.items(an)
	if len(again) != 1 || again[0].read || again[0].params["count"] != float64(1) || again[0].id <= first.id {
		t.Errorf("a message after the item was read left %+v, want one unread item counting one, renewed so it lists first", again)
	}

	w.mustAnnounce(uyen, class, "Chào lớp")
	got = w.items(an)
	if len(got) != 2 || got[0].params["className"] != "Lớp 5A "+w.marker || got[0].target["classId"] != class || got[0].params["count"] != float64(1) {
		t.Errorf("the announcement left An %+v, want a second item, the channel's, first", got)
	}
	if got := w.items(binh); len(got) != 0 {
		t.Errorf("Bình switched message.received off and holds %+v", got)
	}

	w.studentWrites(teacher, an, "Dạ em cảm ơn cô.")
	got = w.items(teacher)
	if len(got) != 1 || got[0].kind != "message.received" || got[0].params["senderName"] != "Học viên An "+w.marker ||
		got[0].params["count"] != float64(1) || got[0].target["route"] != "messages" || got[0].target["studentId"] != an {
		t.Errorf("the teacher holds %+v, want one message.received leading to An's thread", got)
	}
}
