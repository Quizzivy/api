package email_test

import (
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"quizzivy/internal/platform/email"
)

var update = flag.Bool("update", false, "rewrite the golden renders of the notification emails")

const (
	someAttempt    = "0192a7c0-0000-7000-8000-000000000011"
	someAssignment = "0192a7c0-0000-7000-8000-000000000012"
	someClass      = "0192a7c0-0000-7000-8000-000000000013"
	someStudent    = "0192a7c0-0000-7000-8000-000000000014"
	someTeacher    = "0192a7c0-0000-7000-8000-000000000015"
	someThread     = "0192a7c0-0000-7000-8000-000000000016"
	someTest       = "0192a7c0-0000-7000-8000-000000000017"
)

var closesAt = time.Date(2026, 11, 20, 14, 30, 0, 0, time.UTC)

// studentURL is the student host's origin, as appURL is the staff host's.
const studentURL = "https://my.quizzivy.com"

// noticeLinks is the page each notification email's fixture links to, on the
// host the dispatcher sends it from.
var noticeLinks = map[email.Template]string{
	email.AttemptFlagged:      "/teacher/attempts/" + someAttempt,
	email.AssignmentExtended:  "/assignments/" + someAssignment,
	email.AssignmentDueSoon:   "/assignments/" + someAssignment,
	email.AssignmentClosing:   "/teacher/assignments/" + someAssignment,
	email.ResultReady:         "/attempts/" + someAttempt + "/result",
	email.ContentShared:       "/teacher/shared-with-me",
	email.ClassTeacherChanged: "/teacher/classes/" + someClass,
	email.SubmissionsDigest:   "/teacher/assignments/" + someAssignment,
	email.MessageReceived:     "/teacher/messages/student/" + someStudent,
	email.WeeklySummary:       "/teacher",
}

// noticeFixture is tmpl's params as its Preparer fills them, for a recipient
// called name.
func noticeFixture(t *testing.T, tmpl email.Template, name string) email.Params {
	t.Helper()
	switch tmpl {
	case email.AttemptFlagged:
		return email.AttemptFlaggedParams{AttemptID: someAttempt, FullName: name, StudentName: "Lê Hoàng Nam", Title: "Đề giữa kỳ", FocusLost: 3}
	case email.AssignmentExtended:
		return email.AssignmentExtendedParams{AssignmentID: someAssignment, FullName: name, Title: "Đề giữa kỳ", ClosesAt: closesAt}
	case email.AssignmentDueSoon:
		return email.AssignmentDueSoonParams{AssignmentID: someAssignment, Lead: email.LeadDay, FullName: name, Title: "Đề giữa kỳ", ClosesAt: closesAt}
	case email.AssignmentClosing:
		return email.AssignmentClosingParams{AssignmentID: someAssignment, FullName: name, Title: "Đề giữa kỳ", ClosesAt: closesAt, NotSubmitted: 4}
	case email.ResultReady:
		return email.ResultReadyParams{AttemptID: someAttempt, FullName: name, Title: "Đề giữa kỳ"}
	case email.ContentShared:
		return email.ContentSharedParams{Kind: email.SharedTest, ResourceID: someTest, FullName: name, SharerName: "Nguyễn Thị Lan", Name: "Đề giữa kỳ", Access: email.SharedEdit}
	case email.ClassTeacherChanged:
		return email.ClassTeacherChangedParams{ClassID: someClass, FullName: name, ClassName: "IELTS Foundation A"}
	case email.SubmissionsDigest:
		return email.SubmissionsDigestParams{AssignmentID: someAssignment, From: time.Date(2026, 11, 20, 7, 0, 0, 0, time.UTC), FullName: name, Title: "Đề giữa kỳ", Count: 6, ToGrade: 2}
	case email.MessageReceived:
		return email.MessageReceivedParams{ConversationID: someThread, StudentID: someStudent, FullName: name, SenderName: "Lê Hà My", Count: 1}
	case email.WeeklySummary:
		return email.WeeklySummaryParams{WeekOf: time.Date(2026, 11, 8, 17, 0, 0, 0, time.UTC), Count: 12, ToGrade: 3, Flagged: 1, ClassNames: []string{"IELTS Foundation A", "TOEIC 2"}, FullName: name}
	}
	t.Fatalf("no fixture for template %q", tmpl)
	return nil
}

func notices() []email.Template {
	var out []email.Template
	for _, tmpl := range email.Templates() {
		if email.Prepared(tmpl) {
			out = append(out, tmpl)
		}
	}
	return out
}

// TestTheTenNotificationEmailsArePreparedAndEachHasItsKillSwitch fails when a
// notification email is added without being Prepared (its row holds ids, so
// it cannot render without its Preparer), or when the names
// NOTIFY_EMAIL_EVENTS takes drift from the plan's ten.
func TestTheTenNotificationEmailsArePreparedAndEachHasItsKillSwitch(t *testing.T) {
	want := []email.Template{
		email.AttemptFlagged, email.AssignmentExtended, email.AssignmentDueSoon, email.AssignmentClosing, email.ResultReady,
		email.ContentShared, email.ClassTeacherChanged, email.SubmissionsDigest, email.MessageReceived, email.WeeklySummary,
	}
	if got := notices(); !slices.Equal(got, want) {
		t.Errorf("the Prepared templates are %v, want %v", got, want)
	}
	events := []string{
		"attempt.flagged", "assignment.extended", "assignment.due_soon", "assignment.closing", "result.ready",
		"content.shared", "class.teacher_changed", "attempt.submitted", "message.received", "summary.weekly",
	}
	if got := email.NotifyEvents(); !slices.Equal(got, events) {
		t.Errorf("NOTIFY_EMAIL_EVENTS takes %v, want %v", got, events)
	}
	for _, tmpl := range []email.Template{email.AccountInvite, email.PasswordResetLink, email.SignInInvite, email.DataExportReady, email.LeadReceived} {
		if email.Prepared(tmpl) || email.Event(tmpl) != "" {
			t.Errorf("R5's %s changed: prepared %v, event %q", tmpl, email.Prepared(tmpl), email.Event(tmpl))
		}
	}
	for _, tmpl := range want {
		if email.NeedsSecret(tmpl) {
			t.Errorf("%s carries a secret", tmpl)
		}
	}
}

// TestEveryNotificationEmailEndsWithALinkToTheRecipientsOwnSettings fails
// when a notification email loses its footer, or links a teacher to the
// student's settings or a student to the teacher workspace's.
func TestEveryNotificationEmailEndsWithALinkToTheRecipientsOwnSettings(t *testing.T) {
	r, err := email.NewRenderer()
	if err != nil {
		t.Fatal(err)
	}
	words := map[string]string{"vi": "Chọn loại email bạn muốn nhận", "en": "Change which emails you get"}
	for _, tmpl := range notices() {
		for _, locale := range email.Locales {
			for _, staff := range []bool{true, false} {
				l := letterFor(t, tmpl, locale, "An")
				l.Staff = staff
				got, err := r.Render(l)
				if err != nil {
					t.Fatalf("%s/%s: %v", tmpl, locale, err)
				}
				settings := appURL + "/settings/notifications"
				if staff {
					settings = appURL + "/teacher/settings/notifications"
				}
				if !strings.HasSuffix(got.Text, words[locale]+":\n"+settings+"\n") {
					t.Errorf("%s/%s (staff %v): the text part does not end with the footer to %s:\n%s", tmpl, locale, staff, settings, got.Text)
				}
				if !strings.Contains(got.HTML, `<a href="`+settings+`">`+words[locale]+`</a>`) {
					t.Errorf("%s/%s (staff %v): the HTML part has no footer link to %s", tmpl, locale, staff, settings)
				}
			}
		}
	}
}

// forbidden are the fields no notification email's params may have: a
// message's body or excerpt, a file's name, a join code, a password or a
// score (77-r7.md, T-R7.13).
var forbidden = []string{"body", "excerpt", "filename", "joincode", "code", "password", "secret", "token", "score", "percent", "points", "band", "answer"}

func TestNoNotificationEmailCanCarryABodyACodeAPasswordOrAScore(t *testing.T) {
	for _, tmpl := range notices() {
		typ := reflect.TypeOf(noticeFixture(t, tmpl, "An"))
		for i := range typ.NumField() {
			name := strings.ToLower(typ.Field(i).Name)
			for _, word := range forbidden {
				if strings.Contains(name, word) {
					t.Errorf("%s's params have the field %s", tmpl, typ.Field(i).Name)
				}
			}
		}
	}
}

func TestAMessageEmailNamesTheSenderAndTheClassAndLinksToTheThread(t *testing.T) {
	r, err := email.NewRenderer()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		params email.MessageReceivedParams
		link   string
		says   map[string]string
	}{
		{email.MessageReceivedParams{ConversationID: someThread, StudentID: someStudent, SenderName: "Lê Hà My", Count: 3},
			"/teacher/messages/student/" + someStudent,
			map[string]string{"vi": "Bạn có 3 tin nhắn chưa đọc", "en": "You have 3 unread messages"}},
		{email.MessageReceivedParams{ConversationID: someThread, TeacherID: someTeacher, SenderName: "Cô Thương", Count: 1},
			"/messages/teacher/" + someTeacher,
			map[string]string{"vi": "Cô Thương đã gửi tin nhắn cho bạn.", "en": "Cô Thương sent you a message."}},
		{email.MessageReceivedParams{ConversationID: someThread, ClassID: someClass, ClassName: "IELTS Foundation A", SenderName: "Cô Thương", Count: 1},
			"/messages/class/" + someClass,
			map[string]string{"vi": "thông báo mới trong lớp IELTS Foundation A", "en": "new announcement in IELTS Foundation A"}},
	} {
		for _, locale := range email.Locales {
			got, err := r.Render(email.Letter{Locale: locale, Org: "Trung tâm", AppURL: appURL, Params: c.params})
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(got.Text, appURL+c.link+"\n") || !strings.Contains(got.Text, c.says[locale]) {
				t.Errorf("%s: the message email lacks %q or the link %s:\n%s", locale, c.says[locale], c.link, got.Text)
			}
			if !strings.Contains(got.Subject, c.params.SenderName) && !strings.Contains(got.Subject, c.params.ClassName) {
				t.Errorf("%s: the subject %q names neither the sender nor the class", locale, got.Subject)
			}
		}
	}
}

// TestASharersMessageIsEscapedAndShownOnlyWhenWritten fails when the
// sharer's own words, which the share email carries (DG-90), reach the HTML
// part unescaped, or when an empty paragraph stands where there is none.
func TestASharersMessageIsEscapedAndShownOnlyWhenWritten(t *testing.T) {
	r, err := email.NewRenderer()
	if err != nil {
		t.Fatal(err)
	}
	message := `Dùng cho lớp tối <b>nhé</b>`
	shared := noticeFixture(t, email.ContentShared, "An").(email.ContentSharedParams)
	shared.Message = &message
	got, err := r.Render(email.Letter{Locale: "vi", Org: "Trung tâm", AppURL: appURL, Params: shared, Staff: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Text, "\n\nLời nhắn: "+message+"\n\n") || strings.Contains(got.HTML, "<b>nhé</b>") || !strings.Contains(got.HTML, "&lt;b&gt;nhé&lt;/b&gt;") {
		t.Errorf("the sharer's message is missing or unescaped:\n%s\n%s", got.Text, got.HTML)
	}
	shared.Message = nil
	got, err = r.Render(email.Letter{Locale: "vi", Org: "Trung tâm", AppURL: appURL, Params: shared, Staff: true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got.Text, "Lời nhắn") || strings.Contains(got.Text, "\n\n\n") || strings.Contains(got.HTML, "<p></p>") {
		t.Errorf("a share without a message renders a gap or a label:\n%s", got.Text)
	}
}

// golden lists the renders kept in testdata/golden: every notification
// email with its fixture, and the variants whose wording differs.
func golden(t *testing.T) map[string]email.Letter {
	t.Helper()
	saigon, err := time.LoadLocation("Asia/Ho_Chi_Minh")
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]email.Letter{}
	add := func(name string, p email.Params, staff bool) {
		host := studentURL
		if staff {
			host = appURL
		}
		for _, locale := range email.Locales {
			out[name+"."+locale] = email.Letter{Locale: locale, Org: "Trung tâm Ánh Dương", AppURL: host, Params: p, Zone: saigon, Staff: staff}
		}
	}
	for _, tmpl := range notices() {
		add(string(tmpl), noticeFixture(t, tmpl, "Nguyễn Văn An"), email.StaffOnly(tmpl) || tmpl == email.MessageReceived)
	}
	soon := noticeFixture(t, email.AssignmentDueSoon, "Nguyễn Văn An").(email.AssignmentDueSoonParams)
	soon.Lead = email.LeadHour
	add("assignment_due_soon.hour", soon, false)
	previous := noticeFixture(t, email.ClassTeacherChanged, "Nguyễn Văn An").(email.ClassTeacherChangedParams)
	previous.Previous, previous.TeacherName = true, "Trần Minh Khoa"
	add("class_teacher_changed.previous", previous, true)
	message := "Cô dùng bộ này cho lớp tối nhé."
	set := noticeFixture(t, email.ContentShared, "Nguyễn Văn An").(email.ContentSharedParams)
	set.Kind, set.Access, set.Message = email.SharedQuestionSet, email.SharedUse, &message
	add("content_shared.set", set, true)
	announcement := email.MessageReceivedParams{ConversationID: someThread, ClassID: someClass, FullName: "Nguyễn Văn An", SenderName: "Cô Thương", ClassName: "IELTS Foundation A", Count: 2}
	add("message_received.announcement", announcement, false)
	one := noticeFixture(t, email.SubmissionsDigest, "Nguyễn Văn An").(email.SubmissionsDigestParams)
	one.Count, one.ToGrade = 1, 0
	add("submissions_digest.one", one, true)
	return out
}

// TestNotificationEmailsMatchTheirGoldenRenders fails when a notification
// email's subject, text or HTML changes in either language. A deliberate
// change is made with go test -run TestNotificationEmailsMatchTheirGoldenRenders
// -update, and the new files are read in review: they are the copy the owner
// signs off.
func TestNotificationEmailsMatchTheirGoldenRenders(t *testing.T) {
	r, err := email.NewRenderer()
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join("testdata", "golden")
	if *update {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for name, letter := range golden(t) {
		got, err := r.Render(letter)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		rendered := "Subject: " + got.Subject + "\n\n" + got.Text + "\n----- html -----\n" + got.HTML
		path := filepath.Join(dir, name+".golden")
		if *update {
			if err := os.WriteFile(path, []byte(rendered), 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s: %v (run with -update to write it)", name, err)
		}
		if string(want) != rendered {
			t.Errorf("%s renders differently from %s:\n%s", name, path, rendered)
		}
	}
	files, err := filepath.Glob(filepath.Join(dir, "*.golden"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != len(golden(t)) {
		t.Errorf("%d golden files for %d renders: remove the files no render writes", len(files), len(golden(t)))
	}
}
