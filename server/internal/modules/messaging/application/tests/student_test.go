//go:build integration

package application_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"quizzivy/internal/modules/messaging/application/command"
	"quizzivy/internal/modules/messaging/domain"
	messagingrepo "quizzivy/internal/modules/messaging/repositories"
	"quizzivy/internal/platform/db"
	"quizzivy/internal/shared/access"
)

func TestTheStudentEligibilityTableOnTheDatabase(t *testing.T) {
	w := newWorld(t)
	lanID, minhID, hoaID := w.user("Cô Lan", "teacher"), w.user("Thầy Minh", "teacher"), w.user("Cô Hoa", "teacher")
	lan, hoa := by(lanID), by(hoaID)
	mine, left, archived := w.class(lanID, "Lớp 10A"), w.class(hoaID, "Lớp 11B"), w.class(minhID, "Lớp 12C")

	student := w.user("Học viên An", "student")
	w.enrol(mine, student)
	w.enrol(left, student)
	w.remove(left, student)
	archivedWithThread := w.user("Học viên Dương", "student")
	w.enrol(archived, student)
	w.enrol(archived, archivedWithThread)
	w.mustAsStudent(archivedWithThread, minhID, "Em chào thầy ạ.")
	w.archive(archived)

	formerWithThread := w.user("Học viên Bình", "student")
	w.enrol(left, formerWithThread)
	w.mustDirect(hoa, formerWithThread, "Em nhớ ôn bài nhé.")
	w.remove(left, formerWithThread)

	writtenFirst := w.user("Học viên Chi", "student", lanID)
	w.mustDirect(lan, writtenFirst, "Chào em, cô là giáo viên mới của em.")

	disabledTeacher := w.user("Thầy bị khoá", "teacher")
	w.enrol(w.class(disabledTeacher, "Lớp có thầy bị khoá"), student)
	w.disable(disabledTeacher)

	demoted := w.user("Giáo viên thành học viên", "teacher")
	w.enrol(w.class(demoted, "Lớp của người bị đổi vai trò"), formerWithThread)
	w.mustDirect(by(demoted), formerWithThread, "Chào em.")
	w.exec(`UPDATE app.users SET role_id = (SELECT id FROM app.roles WHERE builtin_key = 'student') WHERE id = $1::uuid`, demoted)

	classmate := w.user("Bạn cùng lớp", "student")
	w.enrol(mine, classmate)

	staffMember := w.user("Trợ giảng học cùng lớp", "teacher")
	w.enrol(mine, staffMember)

	creator := w.user("Cô tạo tài khoản", "teacher")
	created := w.user("Học viên cô tạo", "student", creator)

	for name, c := range map[string]struct {
		student, teacher string
		read, post       error
		evidence         string
	}{
		"the teacher of a class the student is in":  {student, lanID, nil, nil, "a class of theirs now"},
		"the teacher of a class the student left":   {student, hoaID, domain.ErrNotFound, domain.ErrNotFound, "no writing first to a former teacher"},
		"a former teacher with a thread":            {formerWithThread, hoaID, nil, nil, "the thread outlives the class"},
		"a teacher who wrote first":                 {writtenFirst, lanID, nil, nil, "the thread they started"},
		"the teacher of an archived class":          {student, minhID, domain.ErrNotFound, domain.ErrNotFound, "an archived class lets no one write first"},
		"an archived class's teacher with a thread": {archivedWithThread, minhID, nil, nil, "the thread outlives the archive"},
		"a teacher who never taught the student":    {classmate, hoaID, domain.ErrNotFound, domain.ErrNotFound, "no class, no thread"},
		"the staff member who created the account":  {created, creator, domain.ErrNotFound, domain.ErrNotFound, "creating is staff's tie, never the student's"},
		"a disabled teacher of the student's class": {student, disabledTeacher, nil, domain.ErrNotAllowed, "readable, no post"},
		"a thread whose teacher is no longer staff": {formerWithThread, demoted, nil, domain.ErrNotAllowed, "the thread stays readable"},
		"a classmate":                                     {student, classmate, domain.ErrNotFound, domain.ErrNotFound, "a student teaches no class"},
		"the student naming themselves":                   {student, student, domain.ErrNotFound, domain.ErrNotFound, "no thread with oneself"},
		"an id no account has":                            {student, uuid.NewString(), domain.ErrNotFound, domain.ErrNotFound, "missing"},
		"a staff member sitting in a class, as a student": {staffMember, lanID, domain.ErrNotFound, domain.ErrNotFound, "the caller must be a student to start"},
		"another student's teacher, through their thread": {student, demoted, domain.ErrNotFound, domain.ErrNotFound, "a thread is its two participants'"},
	} {
		thread, err := w.studentThread(c.student, c.teacher, domain.Cursor{})
		wantErr(t, name+": read ("+c.evidence+")", err, c.read)
		if err == nil && thread.Teacher.CanPost != (c.post == nil) {
			t.Errorf("%s: canPost = %v, want %v", name, thread.Teacher.CanPost, c.post == nil)
		}
		_, err = w.asStudent(c.student, c.teacher, "Em chào thầy cô ạ.")
		wantErr(t, name+": post ("+c.evidence+")", err, c.post)

		facts, err := messagingrepo.NewPostgres(db.NewContext(w.tx)).StudentFacts(context.Background(), c.student, c.teacher)
		if err != nil {
			t.Fatal(err)
		}
		rules := domain.Eligibility{}
		if got := rules.Teacher(facts); got != (c.read == nil) {
			t.Errorf("%s: the manager's Teacher = %v on %+v, want %v", name, got, facts, c.read == nil)
		}
	}

	// An archived class is not named, so its teacher reads "Former teacher"
	// in the thread that outlived it.
	if header, err := w.studentThread(archivedWithThread, minhID, domain.Cursor{}); err != nil || len(header.Teacher.ClassNames) != 0 || !header.Teacher.Former {
		t.Errorf("the archived class's teacher in the header: %+v, %v, want no class names and former", header.Teacher, err)
	}
}

func TestAStudentsFirstPostMakesTheThreadTheTeacherReads(t *testing.T) {
	w := newWorld(t)
	teacher := w.user("Cô Mai", "teacher")
	mai := by(teacher)
	class := w.class(teacher, "Lớp 7B")
	student := w.user("Học viên Phúc", "student")
	w.enrol(class, student)

	empty, err := w.studentThread(student, teacher, domain.Cursor{})
	if err != nil {
		t.Fatal(err)
	}
	if len(empty.Page.Messages) != 0 || empty.Page.LatestID != nil || !empty.Teacher.CanPost ||
		!slices.Equal(empty.Teacher.ClassNames, []string{"Lớp 7B " + w.marker}) || empty.Teacher.Former {
		t.Errorf("the thread before the first post: %+v", empty)
	}
	if empty.Teacher.AvatarURL == nil || *empty.Teacher.AvatarURL == "" {
		t.Errorf("the teacher's photo is not signed: %+v", empty.Teacher)
	}

	clientID := uuid.NewString()
	post := command.PostAsStudent{StudentID: student, TeacherID: teacher, ClientID: clientID, Body: "  Em chào cô, em xin nghỉ buổi mai ạ.\n"}
	first, err := w.app.Commands.PostAsStudent.Handle(context.Background(), post)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Created || !first.Message.Mine || first.Message.Sender.ID != student || *first.Message.Body != "Em chào cô, em xin nghỉ buổi mai ạ." {
		t.Errorf("the first post: %+v, want made, the student's own, trimmed", first)
	}
	again, err := w.app.Commands.PostAsStudent.Handle(context.Background(), post)
	if err != nil || again.Created || again.Message.ID != first.Message.ID {
		t.Errorf("the same clientId again: %+v, %v, want the message it made", again, err)
	}
	other := w.user("Thầy Quang", "teacher")
	w.enrol(w.class(other, "Lớp 7C"), student)
	_, err = w.app.Commands.PostAsStudent.Handle(context.Background(), command.PostAsStudent{StudentID: student, TeacherID: other, ClientID: clientID, Body: "Em chào thầy"})
	wantErr(t, "the clientId reused for another teacher", err, domain.ErrClientIDTaken)
	_, err = w.asStudent(student, teacher, " \n\t")
	wantErr(t, "a blank body", err, domain.ErrEmpty)

	var conversations int
	w.value(`SELECT count(*) FROM app.conversations WHERE kind = 'direct' AND student_id = $1::uuid`, &conversations, student)
	if conversations != 1 {
		t.Errorf("the student has %d threads, want the one the first post made", conversations)
	}

	staff, err := w.thread(teacher, student, domain.Cursor{})
	if err != nil {
		t.Fatal(err)
	}
	if len(staff.Page.Messages) != 1 || staff.Page.Messages[0].Side != domain.SideStudent || staff.Page.Messages[0].Mine {
		t.Errorf("the teacher reads %+v, want the student's message on the student side", staff.Page.Messages)
	}
	reply := w.mustDirect(mai, student, "Cô biết rồi, em nghỉ ngơi nhé.")
	thread, err := w.studentThread(student, teacher, domain.Cursor{})
	if err != nil {
		t.Fatal(err)
	}
	if len(thread.Page.Messages) != 2 || thread.Page.Messages[1].ID != reply.ID || thread.Page.Messages[1].Mine ||
		thread.Page.Messages[1].Sender.Name != "Cô Mai "+w.marker || thread.Page.Messages[1].Sender.AvatarURL == nil {
		t.Errorf("the student reads %+v, want their post, then the teacher's reply, signed", thread.Page.Messages)
	}

	unread := func() int {
		for _, th := range w.studentList(student) {
			if th.Kind == domain.KindDirect && th.TeacherID == teacher {
				return th.Unread
			}
		}
		t.Fatal("the thread is not listed")
		return -1
	}
	if n := unread(); n != 1 {
		t.Errorf("the student's unread = %d, want the teacher's reply alone", n)
	}
	if err := w.studentRead(student, command.MarkReadAsStudent{TeacherID: teacher, MessageID: reply.ID}); err != nil {
		t.Fatal(err)
	}
	if err := w.studentRead(student, command.MarkReadAsStudent{TeacherID: teacher, MessageID: first.Message.ID}); err != nil {
		t.Errorf("an older pointer: %v, want a no-op", err)
	}
	if n := unread(); n != 0 {
		t.Errorf("the student's unread after reading = %d, want 0", n)
	}
	wantErr(t, "a pointer to no message", w.studentRead(student, command.MarkReadAsStudent{TeacherID: teacher, MessageID: uuid.NewString()}), domain.ErrNotInThread)
	wantErr(t, "a pointer in a thread the student may not read", w.studentRead(student, command.MarkReadAsStudent{TeacherID: w.user("Thầy lạ", "teacher"), MessageID: reply.ID}), domain.ErrNotFound)

	var audited int
	w.value(`SELECT count(*) FROM app.audit_log WHERE actor_user_id = $1::uuid`, &audited, student)
	if audited != 0 {
		t.Errorf("the student's posts and pointers wrote %d audit rows, want none (D17)", audited)
	}
}

func TestTwoStudentsOfOneTeacherEachReadTheirOwnThread(t *testing.T) {
	w := newWorld(t)
	teacher := w.user("Cô Ngọc", "teacher")
	ngoc := by(teacher)
	class := w.class(teacher, "Lớp 8A")
	a, b := w.user("Học viên A", "student"), w.user("Học viên B", "student")
	w.enrol(class, a)
	w.enrol(class, b)
	toA, toB := w.mustDirect(ngoc, a, "Tin cho A"), w.mustDirect(ngoc, b, "Tin cho B")
	fromB := w.mustAsStudent(b, teacher, "B trả lời cô")

	thread, err := w.studentThread(a, teacher, domain.Cursor{})
	if err != nil {
		t.Fatal(err)
	}
	if len(thread.Page.Messages) != 1 || thread.Page.Messages[0].ID != toA.ID {
		t.Errorf("A reads %+v, want the teacher's message to A alone", thread.Page.Messages)
	}
	for _, id := range []string{toB.ID, fromB.ID} {
		wantErr(t, "A naming a message of B's thread as a pointer", w.studentRead(a, command.MarkReadAsStudent{TeacherID: teacher, MessageID: id}), domain.ErrNotInThread)
	}
	for _, th := range w.studentList(a) {
		if th.Preview != nil && th.Preview.Text != nil && *th.Preview.Text != "Tin cho A" {
			t.Errorf("A's list previews %q", *th.Preview.Text)
		}
	}
}

func TestAStudentsListNamesTheirTeachersAndTheirAnnouncedClasses(t *testing.T) {
	w := newWorld(t)
	lanID, minhID, hoaID := w.user("Cô Lan", "teacher"), w.user("Thầy Minh", "teacher"), w.user("Cô Hoa", "teacher")
	maiID := w.user("Cô Mai", "teacher")
	lan, hoa := by(lanID), by(hoaID)
	english, maths, quiet := w.class(lanID, "Tiếng Anh"), w.class(lanID, "Ngữ pháp"), w.class(minhID, "Toán")
	former, archived := w.class(hoaID, "Văn"), w.class(lanID, "Lớp đã lưu trữ")
	student := w.user("Học viên Dũng", "student", maiID)
	for _, c := range []string{english, maths, quiet, former, archived} {
		w.enrol(c, student)
	}
	w.mustDirect(hoa, student, "Em đọc bài này nhé.")
	w.remove(former, student)
	w.mustAsStudent(student, lanID, "Em chào cô.")
	w.mustDirect(by(maiID), student, "Cô đã tạo tài khoản cho em.")
	w.mustDirect(lan, student, "Chào em.")
	w.mustAnnounce(lan, english, "Mai lớp nghỉ.")
	w.mustAnnounce(lan, archived, "Lớp này đã kết thúc.")
	w.archive(archived)
	w.mustAnnounce(hoa, former, "Lớp Văn có bài mới.")
	w.exec(`UPDATE app.conversations SET last_message_at = now() - make_interval(mins => CASE
		WHEN staff_id = $1::uuid THEN 3 WHEN class_id = $2::uuid THEN 1 ELSE 2 END)
		WHERE (kind = 'direct' AND student_id = $3::uuid) OR (kind = 'class' AND class_id = $2::uuid)`, hoaID, english, student)

	threads := w.studentList(student)
	var titles []string
	byKey := map[string]domain.StudentThread{}
	for _, th := range threads {
		titles = append(titles, th.Title)
		byKey[string(th.Kind)+th.ClassID+th.TeacherID] = th
	}
	want := []string{"Tiếng Anh " + w.marker, "Cô Lan " + w.marker, "Cô Mai " + w.marker, "Cô Hoa " + w.marker}
	if !slices.Equal(titles, want) {
		t.Fatalf("the student's list: %v, want the announced class, then Lan's and Mai's threads (one moment, by title), then Hoa's, newest first; no channel without a post, no archived class and none of a class the student left", titles)
	}
	if th := byKey["direct"+lanID]; !slices.Equal(th.ClassNames, []string{"Ngữ pháp " + w.marker, "Tiếng Anh " + w.marker}) || th.Former ||
		th.Unread != 1 || th.Preview == nil || th.Preview.Mine || *th.Preview.Text != "Chào em." || th.AvatarURL == nil {
		t.Errorf("Lan's row: %+v, want 'Your teacher · ' her classes that are not archived, the reply unread and previewed, her photo", th)
	}
	if th := byKey["direct"+hoaID]; !th.Former || len(th.ClassNames) != 0 || th.Unread != 1 {
		t.Errorf("Hoa's row: %+v, want 'Former teacher': no class of hers now", th)
	}
	// The staff member who created the account and wrote first never taught
	// the student: no class of theirs and no tie, so neither "Your teacher ·
	// {classes}" nor "Former teacher". The client reads it as "Your teacher"
	// alone (the owner, 2026-11-17).
	if th := byKey["direct"+maiID]; len(th.ClassNames) != 0 || th.Former || th.Unread != 1 {
		t.Errorf("Mai's row: %+v, want no class names, not former, her message unread", th)
	}
	if header, err := w.studentThread(student, maiID, domain.Cursor{}); err != nil || len(header.Teacher.ClassNames) != 0 || header.Teacher.Former || !header.Teacher.CanPost {
		t.Errorf("Mai's thread header: %+v, %v, want no class names, not former, open to a reply", header.Teacher, err)
	}
	if th := byKey["class"+english]; th.TeacherID != "" || len(th.ClassNames) != 0 || th.Former || th.AvatarURL != nil || th.Unread != 1 || *th.Preview.Text != "Mai lớp nghỉ." {
		t.Errorf("the class row: %+v, want 'Class announcements' with its one unread post", th)
	}
	if _, listed := byKey["direct"+minhID]; listed {
		t.Error("a teacher with no thread is listed")
	}
	if outsider := w.studentList(w.user("Học viên khác", "student")); len(outsider) != 0 {
		t.Errorf("a student with no class and no thread lists %+v", outsider)
	}
}

func TestAStudentReadsTheirClassChannelWithoutSeenBy(t *testing.T) {
	w := newWorld(t)
	teacher, other := w.user("Thầy Tuấn", "teacher"), w.user("Cô Vy", "teacher")
	tuan := by(teacher)
	class, theirs := w.class(teacher, "Lớp 6A"), w.class(other, "Lớp 6B")
	student, outsider := w.user("Học viên Hà", "student"), w.user("Học viên ngoài", "student")
	w.enrol(class, student)
	w.enrol(theirs, outsider)

	before, err := w.studentChannel(student, class, domain.Cursor{})
	if err != nil || len(before.Page.Messages) != 0 || before.Channel.Teacher.ID != teacher || before.Channel.Teacher.AvatarURL == nil {
		t.Fatalf("the channel before any post: %+v, %v", before, err)
	}
	first := w.mustAnnounce(tuan, class, "Chào cả lớp.")
	second := w.mustAnnounce(tuan, class, "Thứ sáu kiểm tra.")
	channel, err := w.studentChannel(student, class, domain.Cursor{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if channel.Channel.Name != "Lớp 6A "+w.marker || len(channel.Page.Messages) != 1 || channel.Page.Messages[0].ID != second.ID ||
		channel.Page.NextBefore == nil || *channel.Page.NextBefore != second.ID || *channel.Page.LatestID != second.ID || channel.Page.Messages[0].Mine {
		t.Errorf("the student's newest page of one: %+v", channel)
	}
	older, err := w.studentChannel(student, class, domain.Cursor{Before: second.ID})
	if err != nil || len(older.Page.Messages) != 1 || older.Page.Messages[0].ID != first.ID {
		t.Errorf("the page before the newest: %+v, %v", older, err)
	}
	for name, c := range map[string]struct{ student, class string }{
		"another teacher's class": {student, theirs},
		"a class they are not in": {outsider, class},
		"a missing class":         {student, uuid.NewString()},
	} {
		_, err := w.studentChannel(c.student, c.class, domain.Cursor{})
		wantErr(t, name, err, domain.ErrNotFound)
		wantErr(t, name+": a pointer", w.studentRead(c.student, command.MarkReadAsStudent{ClassID: c.class, MessageID: first.ID}), domain.ErrNotFound)
	}

	if err := w.studentRead(student, command.MarkReadAsStudent{ClassID: class, MessageID: second.ID}); err != nil {
		t.Fatal(err)
	}
	staff, err := w.channel(tuan, class, domain.Cursor{})
	if err != nil {
		t.Fatal(err)
	}
	if seen := staff.Page.Messages[1].SeenBy; seen == nil || seen.Seen != 1 || seen.Of != 1 {
		t.Errorf("the teacher's Seen by after the student read: %+v, want 1 of 1", seen)
	}
	direct := w.mustDirect(tuan, student, "Riêng em nhé.")
	wantErr(t, "a direct message named in the channel", w.studentRead(student, command.MarkReadAsStudent{ClassID: class, MessageID: direct.ID}), domain.ErrNotInThread)

	w.remove(class, student)
	_, err = w.studentChannel(student, class, domain.Cursor{})
	wantErr(t, "the channel of a class the student left", err, domain.ErrNotFound)
}

type flagged struct {
	scope   access.Scope
	student string
	since   time.Time
	calls   int
}

func (f *flagged) FlaggedAttempts(_ context.Context, scope access.Scope, student string, since time.Time) ([]domain.ContextEvent, error) {
	f.scope, f.student, f.since = scope, student, since
	f.calls++
	return []domain.ContextEvent{{AttemptID: uuid.NewString(), AssignmentID: uuid.NewString(), Title: "Đề giữa kỳ", FocusLost: 4, At: since.Add(time.Hour)}}, nil
}

func TestAStaffThreadReadsContextEventsFromADayBeforeItsFirstMessage(t *testing.T) {
	w := newWorld(t)
	events := &flagged{}
	w.app.WithContextEvents(events)
	teacher := w.user("Cô Yến", "teacher")
	yen := by(teacher)
	class := w.class(teacher, "Lớp 4A")
	student := w.user("Học viên Khoa", "student")
	w.enrol(class, student)

	read := func() []domain.ContextEvent {
		thread, err := w.app.Queries.StaffDirectThread.Handle(context.Background(), queryStaffDirect(yen, student))
		if err != nil {
			t.Fatal(err)
		}
		return thread.Events
	}
	if got := read(); len(got) != 0 || events.calls != 0 {
		t.Errorf("a thread with no message: %+v after %d reads of attempts, want none and no read", got, events.calls)
	}
	first := w.mustAsStudent(student, teacher, "Em chào cô.")
	w.mustDirect(yen, student, "Chào em.")
	got := read()
	if len(got) != 1 || got[0].Title != "Đề giữa kỳ" || got[0].FocusLost != 4 {
		t.Errorf("the thread's context events: %+v", got)
	}
	if events.student != student || events.scope != yen.Scope || !events.since.Equal(first.CreatedAt.Add(-24*time.Hour)) {
		t.Errorf("attempts were asked for %s in %+v from %v, want the student, the reader's scope, and a day before the first message (%v)",
			events.student, events.scope, events.since, first.CreatedAt)
	}
	for _, th := range w.mine(w.list(yen, domain.TabDirect, "")) {
		if th.Ref.StudentID == student && th.Unread != 1 {
			t.Errorf("unread = %d, want the student's one message: context events are never unread", th.Unread)
		}
	}
	page, err := w.studentThread(student, teacher, domain.Cursor{})
	if err != nil || len(page.Page.Messages) != 2 {
		t.Errorf("the student's page: %+v, %v, want the two messages and nothing else", page, err)
	}
}
