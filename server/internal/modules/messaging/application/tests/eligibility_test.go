//go:build integration

package application_test

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/google/uuid"

	"quizzivy/internal/modules/messaging/domain"
	messagingrepo "quizzivy/internal/modules/messaging/repositories"
	"quizzivy/internal/platform/db"
	"quizzivy/internal/shared/visibility"
)

// TestTheEligibilityTableOnTheDatabase is F2's staff rule on the database:
// "a thread, or a student, never yourself". Any enabled student may be
// written to, whoever teaches them; a thread stays readable after its
// student is disabled or made staff; a disabled student or a staff account
// with no thread, an anonymised account and the caller are no peer (404).
// visibility.CanMessage and visibility.MessagingPeerIDs say the same as the
// manager, row by row.
func TestTheEligibilityTableOnTheDatabase(t *testing.T) {
	w := newWorld(t)
	teacher, other := w.user("Cô Lan", "teacher"), w.user("Thầy Minh", "teacher")
	lan := by(teacher)
	mine, theirs := w.class(teacher, "Lớp 10A"), w.class(other, "Lớp 11B")

	member := w.user("Học viên trong lớp", "student")
	w.enrol(mine, member)
	removed := w.user("Học viên đã rời lớp", "student")
	w.enrol(mine, removed)
	w.remove(mine, removed)
	removedWithThread := w.user("Học viên rời lớp có cuộc trò chuyện", "student")
	w.enrol(mine, removedWithThread)
	w.mustDirect(lan, removedWithThread, "Em nhớ nộp bài nhé.")
	w.remove(mine, removedWithThread)
	created := w.user("Học viên cô tạo", "student", teacher)
	stranger := w.user("Học viên lớp khác", "student")
	w.enrol(theirs, stranger)
	noClass := w.user("Học viên chưa vào lớp nào", "student")
	disabled := w.user("Học viên bị khoá", "student")
	w.enrol(mine, disabled)
	w.disable(disabled)
	promoted := w.user("Học viên thành giáo viên", "student")
	w.enrol(mine, promoted)
	w.exec(`UPDATE app.users SET role_id = (SELECT id FROM app.roles WHERE builtin_key = 'teacher') WHERE id = $1::uuid`, promoted)
	disabledWithThread := w.user("Học viên có cuộc trò chuyện rồi bị khoá", "student")
	w.enrol(mine, disabledWithThread)
	w.mustDirect(lan, disabledWithThread, "Chào em.")
	w.disable(disabledWithThread)
	staffMade := w.user("Trợ giảng cô tạo", "teacher", teacher)
	createdPromoted := w.user("Học viên cô tạo rồi thành trợ giảng", "student", teacher)
	w.mustDirect(lan, createdPromoted, "Chào em.")
	w.exec(`UPDATE app.users SET role_id = (SELECT id FROM app.roles WHERE builtin_key = 'teacher') WHERE id = $1::uuid`, createdPromoted)
	anonymised := w.user("Học viên đã ẩn danh", "student")
	w.enrol(mine, anonymised)
	w.anonymise(anonymised)
	// D16 deletes an anonymised account's threads in the same transaction;
	// this one keeps its thread, so the rule itself must refuse it.
	anonymisedWithThread := w.user("Học viên ẩn danh còn cuộc trò chuyện", "student")
	w.mustDirect(lan, anonymisedWithThread, "Chào em.")
	w.anonymise(anonymisedWithThread)
	admin := w.user("Quản trị", "admin")

	for name, c := range map[string]struct {
		who      string
		student  string
		read     error
		post     error
		canPost  bool
		evidence string
	}{
		"a member of the caller's class":            {teacher, member, nil, nil, true, "an enabled student"},
		"a removed student":                         {teacher, removed, nil, nil, true, "an enabled student"},
		"a removed student with a thread":           {teacher, removedWithThread, nil, nil, true, "the thread and an enabled student"},
		"a student the caller created":              {teacher, created, nil, nil, true, "an enabled student"},
		"another teacher's student":                 {teacher, stranger, nil, nil, true, "any enabled student, whoever teaches them"},
		"the caller's student, for another teacher": {other, member, nil, nil, true, "any enabled student"},
		"a student in no class":                     {teacher, noClass, nil, nil, true, "any enabled student, in a class or in none"},
		"an admin and the teacher's student":        {admin, member, nil, nil, true, "the Admin's own thread; scope.all plays no part"},
		"a disabled student without a thread":       {teacher, disabled, domain.ErrNotFound, domain.ErrNotFound, false, "a disabled account is a peer only through a thread"},
		"a thread whose student was disabled":       {teacher, disabledWithThread, nil, domain.ErrNotAllowed, false, "the thread stays readable"},
		"a disabled student, for a third person":    {other, disabledWithThread, domain.ErrNotFound, domain.ErrNotFound, false, "the thread is the first teacher's"},
		"a staff account without a thread":          {teacher, promoted, domain.ErrNotFound, domain.ErrNotFound, false, "the strict student predicate"},
		"a staff account the caller created":        {teacher, staffMade, domain.ErrNotFound, domain.ErrNotFound, false, "created, but no student: no peer"},
		"a created student promoted after a thread": {teacher, createdPromoted, nil, domain.ErrNotAllowed, false, "the thread keeps it readable"},
		"an anonymised account":                     {teacher, anonymised, domain.ErrNotFound, domain.ErrNotFound, false, "never a peer"},
		"an anonymised account a thread names":      {teacher, anonymisedWithThread, domain.ErrNotFound, domain.ErrNotFound, false, "never a peer, a thread or not"},
		"the caller naming themselves":              {teacher, teacher, domain.ErrNotFound, domain.ErrNotFound, false, "no thread with oneself"},
		"an id no account has":                      {teacher, uuid.NewString(), domain.ErrNotFound, domain.ErrNotFound, false, "missing"},
	} {
		who := by(c.who)
		if c.who == admin {
			who = byAdmin(admin)
		}
		thread, err := w.thread(c.who, c.student, domain.Cursor{})
		wantErr(t, name+": read ("+c.evidence+")", err, c.read)
		if err == nil && thread.Peer.CanPost != c.canPost {
			t.Errorf("%s: canPost = %v, want %v", name, thread.Peer.CanPost, c.canPost)
		}
		_, err = w.direct(who, c.student, "Chào em, cô gửi lịch học.")
		wantErr(t, name+": post ("+c.evidence+")", err, c.post)

		var canMessage bool
		w.value(fmt.Sprintf(`SELECT coalesce((SELECT %s FROM app.users u WHERE u.id = $2::uuid), false)`, visibility.CanMessage("u", 1)), &canMessage, c.who, c.student)
		if canMessage != c.canPost {
			t.Errorf("%s: visibility.CanMessage = %v, want %v: the Students page's canMessage must be the post's eligibility", name, canMessage, c.canPost)
		}
		facts, err := messagingrepo.NewPostgres(db.NewContext(w.tx)).DirectFacts(context.Background(), c.who, c.student)
		if err != nil {
			t.Fatal(err)
		}
		var peer bool
		w.value(fmt.Sprintf(`SELECT $2::uuid IN %s`, visibility.MessagingPeerIDs(1)), &peer, c.who, c.student)
		if want := (domain.Eligibility{}).Peer(facts); peer != want || peer != (c.read == nil) {
			t.Errorf("%s: visibility.MessagingPeerIDs holds it: %v, the manager's Peer %v, want %v: a peer is whom the thread is readable to", name, peer, want, c.read == nil)
		}
		if got := (domain.Eligibility{}).CanPost(facts); got != canMessage {
			t.Errorf("%s: the manager says %v and visibility.CanMessage %v: they must be one rule", name, got, canMessage)
		}
	}
	var none bool
	w.value(fmt.Sprintf(`SELECT EXISTS (SELECT 1 FROM %s p)`, visibility.MessagingPeerIDs(1)), &none, nil)
	if none {
		t.Errorf("visibility.MessagingPeerIDs holds an account for no staff member: a NULL caller has no peer")
	}
}

// TestTwoStaffMembersWritingToOneStudentEachReadOnlyTheirOwn is D17 under
// F2: teachers A and B and the Admin each address student S, whom only A
// teaches, by S's id, and each reads their own thread with S. A's messages
// never reach B's or the Admin's read, the Admin's read is the Admin's own
// empty thread, and a pointer naming another's message answers as a message
// not in the thread.
func TestTwoStaffMembersWritingToOneStudentEachReadOnlyTheirOwn(t *testing.T) {
	w := newWorld(t)
	a, b, admin := w.user("Cô An", "teacher"), w.user("Thầy Bảo", "teacher"), w.user("Quản trị", "admin")
	class := w.class(a, "Lớp của cô An")
	student := w.user("Học viên Sơn", "student")
	w.enrol(class, student)

	fromA := w.mustDirect(by(a), student, "Tin của cô An")
	fromB := w.mustDirect(by(b), student, "Tin của thầy Bảo")
	for who, c := range map[string]struct {
		id   string
		want []string
	}{
		"A":         {a, []string{fromA.ID}},
		"B":         {b, []string{fromB.ID}},
		"the Admin": {admin, nil},
	} {
		thread, err := w.app.Queries.StaffDirectThread.Handle(context.Background(), queryStaffDirect(byAdminIf(c.id, admin), student))
		if err != nil {
			t.Fatalf("%s reading S: %v", who, err)
		}
		var got []string
		for _, m := range thread.Page.Messages {
			got = append(got, m.ID)
		}
		if !slices.Equal(got, c.want) {
			t.Errorf("%s reads %v of S's threads, want %v: each reads their own thread only", who, got, c.want)
		}
	}
	wantErr(t, "B's pointer naming A's message", w.read(by(b), domain.ThreadRef{Kind: domain.KindDirect, StudentID: student}, fromA.ID), domain.ErrNotInThread)
	wantErr(t, "the Admin's pointer naming A's message", w.read(byAdmin(admin), domain.ThreadRef{Kind: domain.KindDirect, StudentID: student}, fromA.ID), domain.ErrNotInThread)
	if threads := w.list(byAdmin(admin), domain.TabDirect, ""); slices.ContainsFunc(threads, func(th domain.StaffThread) bool { return th.Ref.StudentID == student }) {
		t.Errorf("the Admin lists S before writing to them: %v", threads)
	}
	var count int
	w.value(`SELECT count(*)::int FROM app.conversations WHERE kind = 'direct' AND student_id = $1::uuid`, &count, student)
	if count != 2 {
		t.Errorf("S has %d threads, want A's and B's: reading makes none", count)
	}
}

// TestAThreadShowsTheStudentsOtherClassesByName is otherClassNames: the
// names of the student's current classes that are not archived and that the
// caller does not teach, on the list's direct row, a candidate and the
// thread's header. The caller's own classes stay in classNames and the
// header's classes; an archived class is in neither list of names.
func TestAThreadShowsTheStudentsOtherClassesByName(t *testing.T) {
	w := newWorld(t)
	teacher, other := w.user("Cô Hạnh", "teacher"), w.user("Thầy Phúc", "teacher")
	hanh := by(teacher)
	mine := w.class(teacher, "Lớp Hạnh")
	theirs, second, archived := w.class(other, "Lớp Phúc 1"), w.class(other, "Lớp Phúc 2"), w.class(other, "Lớp Phúc cũ")
	both := w.user("Học viên hai thầy cô", "student")
	w.enrol(mine, both)
	w.enrol(theirs, both)
	w.enrol(second, both)
	w.enrol(archived, both)
	w.archive(archived)
	elsewhere := w.user("Học viên lớp thầy Phúc", "student")
	w.enrol(theirs, elsewhere)
	alone := w.user("Học viên chưa có lớp", "student")
	w.mustDirect(hanh, both, "Chào em")

	others := []string{"Lớp Phúc 1 " + w.marker, "Lớp Phúc 2 " + w.marker}
	rows := map[string]domain.StaffThread{}
	for _, th := range w.mine(w.list(hanh, domain.TabAll, w.marker)) {
		if th.Ref.Kind == domain.KindDirect {
			rows[th.Ref.StudentID] = th
		} else if len(th.OtherClassNames) != 0 {
			t.Errorf("a class row carries otherClassNames: %+v", th)
		}
	}
	for name, c := range map[string]struct {
		student   string
		candidate bool
		mine      []string
		others    []string
	}{
		"the thread with a student two teach": {both, false, []string{"Lớp Hạnh " + w.marker}, others},
		"another teacher's student":           {elsewhere, true, []string{}, []string{"Lớp Phúc 1 " + w.marker}},
		"a student in no class":               {alone, true, []string{}, []string{}},
	} {
		row, ok := rows[c.student]
		if !ok || row.Candidate != c.candidate || !slices.Equal(row.ClassNames, c.mine) || !slices.Equal(row.OtherClassNames, c.others) {
			t.Errorf("%s: %+v (listed %v), want candidate %v, classNames %v, otherClassNames %v", name, row, ok, c.candidate, c.mine, c.others)
		}
		thread, err := w.thread(teacher, c.student, domain.Cursor{})
		if err != nil {
			t.Fatal(err)
		}
		if len(thread.Peer.Classes) != len(c.mine) || !slices.Equal(thread.Peer.OtherClassNames, c.others) || !thread.Peer.CanPost {
			t.Errorf("%s: the header %+v, want %d of the caller's classes and %v", name, thread.Peer, len(c.mine), c.others)
		}
	}
	if found := w.mine(w.list(hanh, domain.TabDirect, "lop phuc 2")); !slices.ContainsFunc(found, func(th domain.StaffThread) bool { return th.Ref.StudentID == both }) ||
		slices.ContainsFunc(found, func(th domain.StaffThread) bool { return th.Ref.StudentID == elsewhere }) {
		t.Errorf("a search on another teacher's class name: %+v, want the student in it alone", found)
	}
	if found := w.mine(w.list(hanh, domain.TabDirect, "lop phuc cu")); len(found) != 0 {
		t.Errorf("a search on an archived class's name finds %+v: an archived class is no current class", found)
	}
}

func TestARemovedStudentsThreadTakesPostsFromBothSides(t *testing.T) {
	w := newWorld(t)
	teacher := w.user("Cô Hoa", "teacher")
	hoa := by(teacher)
	class := w.class(teacher, "Lớp 9C")
	student := w.user("Học viên Nam", "student")
	w.enrol(class, student)
	first := w.mustDirect(hoa, student, "Em ơi, mai em có đi học không?")
	w.remove(class, student)

	reply := w.studentWrites(teacher, student, "Dạ em vẫn đi ạ.")
	second := w.mustDirect(hoa, student, "Cô cảm ơn em.")
	thread, err := w.thread(teacher, student, domain.Cursor{})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, m := range thread.Page.Messages {
		ids = append(ids, m.ID)
	}
	if !slices.Equal(ids, []string{first.ID, reply, second.ID}) {
		t.Errorf("the thread holds %v, want the teacher's, the student's and the teacher's again, oldest first", ids)
	}
	if m := thread.Page.Messages[1]; m.Side != domain.SideStudent || m.Mine || thread.Page.Messages[0].Side != domain.SideStaff || !thread.Page.Messages[0].Mine {
		t.Errorf("sides: %+v", thread.Page.Messages)
	}
	if !thread.Peer.Former || len(thread.Peer.Classes) != 0 || !thread.Peer.CanPost {
		t.Errorf("the removed student's header = %+v, want former, in no class of the teacher's, and open to posts", thread.Peer)
	}
	if thread.Page.LatestID == nil || *thread.Page.LatestID != second.ID {
		t.Errorf("latestId = %v, want %s", thread.Page.LatestID, second.ID)
	}
}

// TestASearchOffersAnyEnabledStudent is the list's search under F2: after
// the threads, the enabled students the caller has no thread with whose
// names, or the names of whose current classes, match, the caller's or
// another teacher's, and students in no class; never a disabled student, a
// staff account or an anonymised one.
func TestASearchOffersAnyEnabledStudent(t *testing.T) {
	w := newWorld(t)
	teacher, other := w.user("Cô Thu", "teacher"), w.user("Thầy Bình", "teacher")
	thu := by(teacher)
	class, theirs := w.class(teacher, "Lớp Tiếng Anh A"), w.class(other, "Lớp Tiếng Anh B")
	former := w.user("Nguyễn Văn Đức", "student")
	w.enrol(class, former)
	w.remove(class, former)
	current := w.user("Trần Thị Đức Anh", "student")
	w.enrol(class, current)
	untaught := w.user("Lê Đức Huy", "student")
	w.enrol(theirs, untaught)
	noClass := w.user("Đỗ Đức Minh", "student")
	disabled := w.user("Phạm Đức Long", "student")
	w.enrol(class, disabled)
	w.disable(disabled)
	staff := w.user("Thầy Đức Trợ giảng", "teacher")
	anonymised := w.user("Hồ Đức Ẩn", "student")
	w.anonymise(anonymised)
	written := w.user("Võ Đức Tài", "student")
	w.enrol(class, written)
	w.mustDirect(thu, written, "Chào em.")

	threads := w.mine(w.list(thu, domain.TabAll, "duc"))
	byStudent := map[string]domain.StaffThread{}
	for _, th := range threads {
		byStudent[th.Ref.StudentID] = th
	}
	if c, ok := byStudent[former]; !ok || !c.Candidate || !c.Former || len(c.ClassNames) != 0 || c.LastMessageAt != nil || c.Preview != nil {
		t.Errorf("the former student, searched accent-free: %+v (listed %v), want a former candidate", c, ok)
	}
	if c, ok := byStudent[current]; !ok || !c.Candidate || c.Former || !slices.Equal(c.ClassNames, []string{"Lớp Tiếng Anh A " + w.marker}) {
		t.Errorf("the current student: %+v (listed %v), want a candidate in the teacher's class", c, ok)
	}
	if c, ok := byStudent[untaught]; !ok || !c.Candidate || c.Former || len(c.ClassNames) != 0 || !slices.Equal(c.OtherClassNames, []string{"Lớp Tiếng Anh B " + w.marker}) {
		t.Errorf("another teacher's student: %+v (listed %v), want a candidate showing the other class's name", c, ok)
	}
	if c, ok := byStudent[noClass]; !ok || !c.Candidate || len(c.ClassNames) != 0 || len(c.OtherClassNames) != 0 {
		t.Errorf("the student in no class: %+v (listed %v), want a candidate in no class", c, ok)
	}
	for name, id := range map[string]string{"the disabled student": disabled, "the staff account": staff, "the anonymised account": anonymised} {
		if _, ok := byStudent[id]; ok {
			t.Errorf("%s is offered: %v", name, threads)
		}
	}
	if th, ok := byStudent[written]; !ok || th.Candidate || th.LastMessageAt == nil {
		t.Errorf("the student with a thread: %+v (listed %v), want their thread, never a candidate", th, ok)
	}
	if last := threads[len(threads)-1]; !last.Candidate {
		t.Errorf("the candidates come after the threads: %+v", threads)
	}
	for _, th := range w.mine(w.list(thu, domain.TabAll, "")) {
		if th.Candidate {
			t.Errorf("without a search a candidate is listed: %+v", th)
		}
	}
	for _, th := range w.mine(w.list(thu, domain.TabClasses, "duc")) {
		if th.Candidate || th.Ref.Kind != domain.KindClass {
			t.Errorf("the classes tab lists %+v", th)
		}
	}
	if byClass := w.mine(w.list(thu, domain.TabDirect, "tieng anh a")); !slices.ContainsFunc(byClass, func(th domain.StaffThread) bool { return th.Ref.StudentID == current }) {
		t.Errorf("a search on the class's name offers none of its students: %+v", byClass)
	}
	if byClass := w.mine(w.list(thu, domain.TabDirect, "tieng anh b")); !slices.ContainsFunc(byClass, func(th domain.StaffThread) bool { return th.Ref.StudentID == untaught }) {
		t.Errorf("a search on another teacher's class's name offers none of its students: %+v", byClass)
	}

	for i := range domain.MaxCandidates + 3 {
		w.enrol(class, w.user(fmt.Sprintf("Học viên số %02d", i), "student"))
	}
	candidates := 0
	for _, th := range w.list(thu, domain.TabDirect, w.marker) {
		if th.Candidate {
			candidates++
		}
	}
	if candidates != domain.MaxCandidates {
		t.Errorf("a search offers %d candidates, want at most %d", candidates, domain.MaxCandidates)
	}
}
