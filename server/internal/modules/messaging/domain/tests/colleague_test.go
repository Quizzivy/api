package domain_test

import (
	"errors"
	"reflect"
	"testing"

	"quizzivy/internal/modules/messaging/domain"
)

// colleague is an enabled account that opens the teacher workspace: a
// teacher, an Admin or a custom staff role with it (F2, T-F2.4).
var colleague = domain.ColleagueFacts{Exists: true, Enabled: true, Workspace: true}

func colleagueWith(change func(*domain.ColleagueFacts)) domain.ColleagueFacts {
	f := colleague
	change(&f)
	return f
}

// TestTheEligibilityTableForAColleagueThread is F2's colleague rule, "a
// thread, or an account that opens the teacher workspace and is not a
// student, never yourself": scope.all and classes take no part.
func TestTheEligibilityTableForAColleagueThread(t *testing.T) {
	rules := domain.Eligibility{}
	for name, c := range map[string]struct {
		facts      domain.ColleagueFacts
		read, post error
	}{
		"a teacher, an Admin or a custom staff role with the workspace": {colleague, nil, nil},
		"a colleague with a thread":                                     {colleagueWith(func(f *domain.ColleagueFacts) { f.Conversation = true }), nil, nil},
		"a role that opens only the Admin console":                      {colleagueWith(func(f *domain.ColleagueFacts) { f.Workspace = false }), domain.ErrNotFound, domain.ErrNotFound},
		"a student": {colleagueWith(func(f *domain.ColleagueFacts) { f.Workspace, f.Student = false, true }), domain.ErrNotFound, domain.ErrNotFound},
		"a student whose role also opens the workspace": {colleagueWith(func(f *domain.ColleagueFacts) { f.Student = true }), domain.ErrNotFound, domain.ErrNotFound},
		"a disabled colleague without a thread":         {colleagueWith(func(f *domain.ColleagueFacts) { f.Enabled = false }), domain.ErrNotFound, domain.ErrNotFound},
		"a disabled colleague with a thread":            {colleagueWith(func(f *domain.ColleagueFacts) { f.Conversation, f.Enabled = true, false }), nil, domain.ErrNotAllowed},
		"a thread whose colleague lost the workspace":   {colleagueWith(func(f *domain.ColleagueFacts) { f.Conversation, f.Workspace = true, false }), nil, domain.ErrNotAllowed},
		"a thread whose colleague became a student":     {colleagueWith(func(f *domain.ColleagueFacts) { f.Conversation, f.Workspace, f.Student = true, false, true }), nil, domain.ErrNotAllowed},
		"an anonymised account":                         {colleagueWith(func(f *domain.ColleagueFacts) { f.Anonymised, f.Enabled = true, false }), domain.ErrNotFound, domain.ErrNotFound},
		"an anonymised account a thread still names":    {colleagueWith(func(f *domain.ColleagueFacts) { f.Anonymised, f.Conversation = true, true }), domain.ErrNotFound, domain.ErrNotFound},
		"an id no account has":                          {domain.ColleagueFacts{}, domain.ErrNotFound, domain.ErrNotFound},
		"the caller naming themselves":                  {colleagueWith(func(f *domain.ColleagueFacts) { f.Self = true }), domain.ErrNotFound, domain.ErrNotFound},
	} {
		if err := rules.ReadColleague(c.facts); !errors.Is(err, c.read) {
			t.Errorf("%s: read = %v, want %v", name, err, c.read)
		}
		if err := rules.PostColleague(c.facts); !errors.Is(err, c.post) {
			t.Errorf("%s: post = %v, want %v", name, err, c.post)
		}
		if got := rules.CanPostColleague(c.facts); got != (c.post == nil) {
			t.Errorf("%s: CanPostColleague = %v, want %v", name, got, c.post == nil)
		}
		if got := rules.Colleague(c.facts); got != (c.read == nil) {
			t.Errorf("%s: Colleague = %v, want %v: a colleague is exactly whom the thread is readable to", name, got, c.read == nil)
		}
	}
}

// TestAColleagueMessageTellsTheOtherMemberOfThePair: a colleague thread's
// item leads to the reader's colleague, whichever of the pair stores low, and
// names no class, no student and no teacher.
func TestAColleagueMessageTellsTheOtherMemberOfThePair(t *testing.T) {
	body := "Chị gửi em đề nghe tuần sau nhé"
	d := domain.Delivery{
		ConversationID: noticeThread, Kind: domain.KindColleague, ColleagueLowID: noticeStaff, ColleagueHighID: noticeOther,
		SenderName: "Cô Thương", Body: &body, Readers: []string{noticeOther},
	}
	want := []domain.Notice{{ReaderID: noticeOther, ConversationID: noticeThread, SenderName: "Cô Thương", Excerpt: body, ColleagueID: noticeStaff}}
	if got := d.Notices(); !reflect.DeepEqual(got, want) {
		t.Errorf("the low member's message tells %+v, want %+v", got, want)
	}
	d.Readers = []string{noticeStaff}
	want = []domain.Notice{{ReaderID: noticeStaff, ConversationID: noticeThread, SenderName: "Cô Thương", Excerpt: body, ColleagueID: noticeOther}}
	if got := d.Notices(); !reflect.DeepEqual(got, want) {
		t.Errorf("the high member's message tells %+v, want %+v", got, want)
	}
}

// TestAColleagueThreadsFileIsItsPairs: only the two staff members of a
// colleague thread are in its pair; no other kind of thread has one.
func TestAColleagueThreadsFileIsItsPairs(t *testing.T) {
	pair := domain.AttachmentThread{Kind: domain.KindColleague, ColleagueLowID: noticeStaff, ColleagueHighID: noticeOther}
	for reader, want := range map[string]bool{noticeStaff: true, noticeOther: true, noticeStudent: false, "": false} {
		if got := pair.InPair(reader); got != want {
			t.Errorf("%q in the pair: %v, want %v", reader, got, want)
		}
	}
	direct := domain.AttachmentThread{Kind: domain.KindDirect, StaffID: noticeStaff, StudentID: noticeStudent, ColleagueLowID: noticeStaff}
	if direct.InPair(noticeStaff) {
		t.Error("a direct thread's staff member is in a colleague pair")
	}
	for reader, other := range map[string]string{noticeStaff: noticeOther, noticeOther: noticeStaff} {
		if got := domain.OtherOf(reader, noticeStaff, noticeOther); got != other {
			t.Errorf("the colleague of %s is %s, want %s", reader, got, other)
		}
	}
}
