//go:build integration

package application_test

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"quizzivy/internal/modules/messaging/domain"
	messagingrepo "quizzivy/internal/modules/messaging/repositories"
	"quizzivy/internal/platform/db"
	"quizzivy/internal/shared/visibility"
)

func TestTheEligibilityTableOnTheDatabase(t *testing.T) {
	w := newWorld(t)
	teacher, other := w.user("Cô Lan", "teacher"), w.user("Thầy Minh", "teacher")
	lan, minh := by(teacher), by(other)
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
	createdDisabled := w.user("Học viên cô tạo rồi bị khoá", "student", teacher)
	w.disable(createdDisabled)
	createdPromoted := w.user("Học viên cô tạo rồi thành trợ giảng", "student", teacher)
	w.mustDirect(lan, createdPromoted, "Chào em.")
	w.exec(`UPDATE app.users SET role_id = (SELECT id FROM app.roles WHERE builtin_key = 'teacher') WHERE id = $1::uuid`, createdPromoted)
	admin := w.user("Quản trị", "admin")

	for name, c := range map[string]struct {
		who      string
		student  string
		read     error
		post     error
		canPost  bool
		evidence string
	}{
		"a member of the caller's class":                {teacher, member, nil, nil, true, "the tie of the enrolment"},
		"a removed student, through messaging_ties":     {teacher, removed, nil, nil, true, "the tie outlives the membership"},
		"a removed student with a thread":               {teacher, removedWithThread, nil, nil, true, "the thread stays open after the tie's class"},
		"a student the caller created":                  {teacher, created, nil, nil, true, "users.created_by"},
		"a student of another teacher's class":          {teacher, stranger, domain.ErrNotFound, domain.ErrNotFound, false, "never taught"},
		"another teacher's own student, for them":       {other, stranger, nil, nil, true, "their tie"},
		"the caller's student, for another teacher":     {other, member, domain.ErrNotFound, domain.ErrNotFound, false, "a tie is per staff member"},
		"a disabled student of the caller's":            {teacher, disabled, nil, domain.ErrNotAllowed, false, "readable, no post"},
		"a tied account that is no longer a student":    {teacher, promoted, nil, domain.ErrNotAllowed, false, "the strict student predicate"},
		"a thread whose student was disabled":           {teacher, disabledWithThread, nil, domain.ErrNotAllowed, false, "the thread stays readable"},
		"a staff account the caller created":            {teacher, staffMade, domain.ErrNotFound, domain.ErrNotFound, false, "created, but no student: no peer"},
		"a student the caller created, disabled":        {teacher, createdDisabled, nil, domain.ErrNotAllowed, false, "a created student stays a peer"},
		"a created student promoted after a thread":     {teacher, createdPromoted, nil, domain.ErrNotAllowed, false, "the thread keeps it readable"},
		"an admin and the teacher's student":            {admin, member, domain.ErrNotFound, domain.ErrNotFound, false, "scope.all does not open a direct thread"},
		"the caller naming themselves":                  {teacher, teacher, domain.ErrNotFound, domain.ErrNotFound, false, "no thread with oneself"},
		"another teacher's student, for a third person": {other, removed, domain.ErrNotFound, domain.ErrNotFound, false, "never taught"},
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
		w.value(fmt.Sprintf(`SELECT %s FROM app.users u WHERE u.id = $2::uuid`, visibility.CanMessage("u", 1)), &canMessage, c.who, c.student)
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

	if threads := w.list(minh, domain.TabDirect, ""); slices.ContainsFunc(threads, func(th domain.StaffThread) bool { return th.Ref.StudentID == member }) {
		t.Errorf("another teacher lists the caller's student %v", threads)
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

func TestASearchOffersAFormerStudentAndNeverOneWithoutATie(t *testing.T) {
	w := newWorld(t)
	teacher, other := w.user("Cô Thu", "teacher"), w.user("Thầy Bình", "teacher")
	thu := by(teacher)
	class, theirs := w.class(teacher, "Lớp Tiếng Anh A"), w.class(other, "Lớp Tiếng Anh B")
	former := w.user("Nguyễn Văn Đức", "student")
	w.enrol(class, former)
	w.remove(class, former)
	current := w.user("Trần Thị Đức Anh", "student")
	w.enrol(class, current)
	untied := w.user("Lê Đức Huy", "student")
	w.enrol(theirs, untied)
	disabled := w.user("Phạm Đức Long", "student")
	w.enrol(class, disabled)
	w.disable(disabled)
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
	for name, id := range map[string]string{"the student without a tie": untied, "the disabled student": disabled} {
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
