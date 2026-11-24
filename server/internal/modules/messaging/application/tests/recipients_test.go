//go:build integration

package application_test

import (
	"context"
	"encoding/base64"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"quizzivy/internal/modules/messaging/application/query"
	"quizzivy/internal/modules/messaging/domain"
	"quizzivy/internal/shared/actor"
)

func (w *world) recipients(who actor.Actor, f domain.RecipientFilter, cursor string) (domain.Recipients, error) {
	w.t.Helper()
	f.ActorID, f.All = who.ID, who.Scope.All
	return w.app.Queries.StudentRecipients.Handle(context.Background(), query.StudentRecipients{Filter: f, Cursor: cursor})
}

func ids(rows []domain.Recipient) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.ID
	}
	return out
}

// TestThePickerListsEveryEnabledStudentByName is listMessageRecipients for
// students (F2): every enabled student, in the caller's class, in another
// teacher's or in none, by folded name; never a disabled student, a staff
// account or an anonymised one. A row names the caller's current classes in
// classNames and the student's other current classes in otherClassNames, an
// archived class in neither, and says whether the caller's thread exists.
// The four filters: none, the caller's classes (any class in their class
// scope, every class under scope.all), one class in that scope (another's
// answers ErrNotFound) and in no current class.
func TestThePickerListsEveryEnabledStudentByName(t *testing.T) {
	w := newWorld(t)
	teacher, other, admin := w.user("Cô Tâm", "teacher"), w.user("Thầy Khoa", "teacher"), w.user("Quản trị", "admin")
	tam := by(teacher)
	mine, theirs, old := w.class(teacher, "Lớp Tâm"), w.class(other, "Lớp Khoa"), w.class(teacher, "Lớp Tâm cũ")
	inMine := w.user("Âu Anh", "student")
	w.enrol(mine, inMine)
	inBoth := w.user("Bùi Bình", "student")
	w.enrol(mine, inBoth)
	w.enrol(theirs, inBoth)
	inTheirs := w.user("Cao Chi", "student")
	w.enrol(theirs, inTheirs)
	noClass := w.user("Đặng Dũng", "student")
	archivedOnly := w.user("Lê Linh", "student")
	w.enrol(old, archivedOnly)
	w.archive(old)
	disabled := w.user("Mai Minh", "student")
	w.enrol(mine, disabled)
	w.disable(disabled)
	w.user("Ngô Nam", "teacher") // a staff account, never listed
	anonymised := w.user("Phan Phúc", "student")
	w.anonymise(anonymised)
	w.mustDirect(tam, inTheirs, "Chào em, cô là giáo viên của trung tâm.")

	all, err := w.recipients(tam, domain.RecipientFilter{Query: w.marker}, "")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{inMine, inBoth, inTheirs, noClass, archivedOnly}; !slices.Equal(ids(all.Rows), want) || all.Next != "" {
		t.Fatalf("the picker lists %v (next %q), want %v by name: every enabled student, never a disabled, staff or anonymised account", ids(all.Rows), all.Next, want)
	}
	for _, c := range []struct {
		name         string
		row          domain.Recipient
		mine, others []string
		threadExists bool
	}{
		{"a student of the caller's class", all.Rows[0], []string{"Lớp Tâm " + w.marker}, []string{}, false},
		{"a student two teachers teach", all.Rows[1], []string{"Lớp Tâm " + w.marker}, []string{"Lớp Khoa " + w.marker}, false},
		{"another teacher's student, written to", all.Rows[2], []string{}, []string{"Lớp Khoa " + w.marker}, true},
		{"a student in no class", all.Rows[3], []string{}, []string{}, false},
		{"a student of an archived class only", all.Rows[4], []string{}, []string{}, false},
	} {
		if !slices.Equal(c.row.ClassNames, c.mine) || !slices.Equal(c.row.OtherClassNames, c.others) || c.row.ThreadExists != c.threadExists || !c.row.CanPost {
			t.Errorf("%s: %+v, want classNames %v, otherClassNames %v, threadExists %v and canPost", c.name, c.row, c.mine, c.others, c.threadExists)
		}
		if c.row.AvatarURL == nil || !strings.HasPrefix(*c.row.AvatarURL, "https://photos.example/") {
			t.Errorf("%s: the photo is not signed: %v", c.name, c.row.AvatarURL)
		}
	}
	if found, err := w.recipients(tam, domain.RecipientFilter{Query: "dang dung " + w.marker}, ""); err != nil || !slices.Equal(ids(found.Rows), []string{noClass}) {
		t.Errorf("an accent-free search: %v, %v, want Đặng Dũng", ids(found.Rows), err)
	}

	for name, c := range map[string]struct {
		who    actor.Actor
		filter domain.RecipientFilter
		want   []string
		err    error
	}{
		"the caller's classes":                    {tam, domain.RecipientFilter{Mine: true}, []string{inMine, inBoth}, nil},
		"one of the caller's classes":             {tam, domain.RecipientFilter{ClassID: mine}, []string{inMine, inBoth}, nil},
		"the caller's archived class":             {tam, domain.RecipientFilter{ClassID: old}, []string{archivedOnly}, nil},
		"another teacher's class":                 {tam, domain.RecipientFilter{ClassID: theirs}, nil, domain.ErrNotFound},
		"a class no one has":                      {tam, domain.RecipientFilter{ClassID: uuid.NewString()}, nil, domain.ErrNotFound},
		"in no class":                             {tam, domain.RecipientFilter{InNoClass: true}, []string{noClass, archivedOnly}, nil},
		"the Admin's classes, under scope.all":    {byAdmin(admin), domain.RecipientFilter{Mine: true}, []string{inMine, inBoth, inTheirs}, nil},
		"another teacher's class, for the Admin":  {byAdmin(admin), domain.RecipientFilter{ClassID: theirs}, []string{inBoth, inTheirs}, nil},
		"the caller's class, for another teacher": {by(other), domain.RecipientFilter{ClassID: mine}, nil, domain.ErrNotFound},
		"a class with mine":                       {tam, domain.RecipientFilter{ClassID: mine, Mine: true}, nil, domain.ErrInvalid},
		"a class in no class":                     {tam, domain.RecipientFilter{ClassID: mine, InNoClass: true}, nil, domain.ErrInvalid},
		"mine in no class":                        {tam, domain.RecipientFilter{Mine: true, InNoClass: true}, nil, domain.ErrInvalid},
	} {
		c.filter.Query = w.marker
		got, err := w.recipients(c.who, c.filter, "")
		wantErr(t, name, err, c.err)
		if c.err == nil && !slices.Equal(ids(got.Rows), c.want) {
			t.Errorf("%s: %v, want %v", name, ids(got.Rows), c.want)
		}
	}
	if theirView, err := w.recipients(by(other), domain.RecipientFilter{Query: w.marker}, ""); err != nil || !slices.Equal(ids(theirView.Rows), ids(all.Rows)) {
		t.Errorf("another teacher's picker: %v, %v, want the same students: every staff member gets the same rows", ids(theirView.Rows), err)
	} else if theirView.Rows[2].ThreadExists || !slices.Equal(theirView.Rows[2].ClassNames, []string{"Lớp Khoa " + w.marker}) {
		t.Errorf("another teacher's row of their own student: %+v, want their class in classNames and no thread: a thread is its two participants'", theirView.Rows[2])
	}

	for name, cursor := range map[string]string{
		"not base64":            "%%%",
		"another list's cursor": base64.RawURLEncoding.EncodeToString([]byte("threads:" + inMine)),
		"no uuid":               base64.RawURLEncoding.EncodeToString([]byte("recipients:" + "an-id")),
	} {
		_, err := w.recipients(tam, domain.RecipientFilter{Query: w.marker}, cursor)
		wantErr(t, "a cursor the picker did not give: "+name, err, domain.ErrInvalid)
	}
}

// TestThePickerPagesByFoldedNameThenID: a page of Limit rows and the cursor
// of the next, which starts after the last row's (folded name, id), so
// students who share a name are each listed once; the last page has no
// cursor, and a cursor stays the same size whatever the name.
func TestThePickerPagesByFoldedNameThenID(t *testing.T) {
	w := newWorld(t)
	teacher := w.user("Cô Yến", "teacher")
	yen := by(teacher)
	var want []string
	for range 5 {
		want = append(want, w.user("Trùng Tên", "student"))
	}
	slices.Sort(want)
	want = append([]string{w.user("Ánh "+strings.Repeat("Đ", 180), "student")}, want...)
	want = append(want, w.user("Ưng Út", "student"))

	var got []string
	cursor := ""
	for page := 0; ; page++ {
		if page > 4 {
			t.Fatalf("the picker never ends: %v", got)
		}
		rows, err := w.recipients(yen, domain.RecipientFilter{Query: w.marker, Limit: 2}, cursor)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows.Rows) > 2 {
			t.Errorf("a page of %d rows, want at most 2", len(rows.Rows))
		}
		got = append(got, ids(rows.Rows)...)
		if rows.Next == "" {
			break
		}
		if len(rows.Next) > 512 {
			t.Errorf("a cursor of %d characters: the contract takes 512 at most", len(rows.Next))
		}
		cursor = rows.Next
	}
	if !slices.Equal(got, want) {
		t.Errorf("the pages list %v, want %v: by folded name, then id, each once", got, want)
	}
	for limit, size := range map[int]int{0: domain.DefaultRecipients, 100: domain.MaxRecipients} {
		f, err := domain.RecipientFilter{Audience: domain.AudienceStudents, Limit: limit}.Check()
		if err != nil || f.Limit != size {
			t.Errorf("limit %d is read as %d (%v), want %d", limit, f.Limit, err, size)
		}
	}
}

// TestACursorNamingAnAccountTheAudienceDoesNotListEndsThePicker: the next
// page reads the cursor's row again only while it is one of the audience's
// own rows, so a cursor naming any other account, forged or one that has
// left the audience since, ends the list, and never places the account
// among the names: a staff account or a disabled student for students, a
// student or an account of the Admin console alone for colleagues.
func TestACursorNamingAnAccountTheAudienceDoesNotListEndsThePicker(t *testing.T) {
	w := newWorld(t)
	me := w.user("Ông Chủ", "teacher")
	w.user("Bùi Học viên", "student")
	w.user("Cao Học viên", "student")
	w.user("Bùi Giáo viên", "teacher")
	w.user("Cao Giáo viên", "teacher")
	staff := w.user("A Giáo viên đứng đầu", "teacher")
	student := w.user("A Học viên đứng đầu", "student")
	disabled := w.user("A Học viên đã khoá", "student")
	w.disable(disabled)
	console := w.holding("A Kiểm toán", w.role("Kiểm toán", "system.audit.read"))

	for name, c := range map[string]struct {
		audience domain.Audience
		row      string
	}{
		"a staff account, for students":       {domain.AudienceStudents, staff},
		"a disabled student, for students":    {domain.AudienceStudents, disabled},
		"a student, for colleagues":           {domain.AudienceColleagues, student},
		"the Admin console alone, colleagues": {domain.AudienceColleagues, console},
		"the caller, for colleagues":          {domain.AudienceColleagues, me},
	} {
		f := domain.RecipientFilter{Query: w.marker}
		var page domain.Recipients
		var err error
		if c.audience == domain.AudienceStudents {
			page, err = w.recipients(by(me), f, domain.RecipientCursor(c.row))
		} else {
			page, err = w.colleagueRecipients(by(me), f, domain.RecipientCursor(c.row))
		}
		if err != nil || len(page.Rows) != 0 || page.Next != "" {
			t.Errorf("%s: %v (next %q), %v, want the list ended", name, ids(page.Rows), page.Next, err)
		}
	}
	if page, err := w.recipients(by(me), domain.RecipientFilter{Query: w.marker}, domain.RecipientCursor(student)); err != nil || len(page.Rows) != 2 {
		t.Errorf("a student's own cursor: %v, %v, want the two students after it", ids(page.Rows), err)
	}
	if page, err := w.colleagueRecipients(by(me), domain.RecipientFilter{Query: w.marker}, domain.RecipientCursor(staff)); err != nil || len(page.Rows) != 2 {
		t.Errorf("a colleague's own cursor: %v, %v, want the two colleagues after it", ids(page.Rows), err)
	}
}
