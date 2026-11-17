package domain_test

import (
	"reflect"
	"strings"
	"testing"

	"quizzivy/internal/modules/messaging/domain"
)

const (
	noticeThread  = "019535d9-0000-7000-8000-00000000c0a1"
	noticeStaff   = "019535d9-0000-7000-8000-00000000057a"
	noticeStudent = "019535d9-0000-7000-8000-0000000005d1"
	noticeOther   = "019535d9-0000-7000-8000-0000000005d2"
	noticeClass   = "019535d9-0000-7000-8000-00000000c1a5"
)

func TestADirectMessageTellsTheOtherSideWhereItLeads(t *testing.T) {
	body := "Chào em,\n\n  mai cô   gửi đề nhé"
	d := domain.Delivery{
		ConversationID: noticeThread, Kind: domain.KindDirect, StaffID: noticeStaff, StudentID: noticeStudent,
		SenderName: "Cô Thương", Body: &body, Readers: []string{noticeStudent},
	}
	want := []domain.Notice{{
		ReaderID: noticeStudent, ConversationID: noticeThread, ToStudent: true, SenderName: "Cô Thương",
		Excerpt: "Chào em, mai cô gửi đề nhé", TeacherID: noticeStaff,
	}}
	if got := d.Notices(); !reflect.DeepEqual(got, want) {
		t.Errorf("the staff member's message tells %+v, want %+v", got, want)
	}

	d.SenderName, d.Readers = "Lê Hoàng Nam", []string{noticeStaff}
	want = []domain.Notice{{
		ReaderID: noticeStaff, ConversationID: noticeThread, SenderName: "Lê Hoàng Nam",
		Excerpt: "Chào em, mai cô gửi đề nhé", StudentID: noticeStudent,
	}}
	if got := d.Notices(); !reflect.DeepEqual(got, want) {
		t.Errorf("the student's message tells %+v, want %+v", got, want)
	}
}

func TestAnAnnouncementTellsEveryReaderWithTheClassesName(t *testing.T) {
	filename := strings.Repeat("Đề ", 30) + ".pdf"
	d := domain.Delivery{
		ConversationID: noticeThread, Kind: domain.KindClass, ClassID: noticeClass, ClassName: "IELTS 6.5",
		StaffID: "", StudentID: "", SenderName: "Cô Thương", Filename: &filename, Readers: []string{noticeStudent, noticeOther},
	}
	got := d.Notices()
	if len(got) != 2 {
		t.Fatalf("the announcement tells %d readers, want 2: %+v", len(got), got)
	}
	excerpt := []rune(strings.Join(strings.Fields(filename), " "))[:domain.ExcerptLength]
	for i, n := range got {
		if n.ReaderID != d.Readers[i] || !n.ToStudent || n.ClassID != noticeClass || n.ClassName == nil || *n.ClassName != "IELTS 6.5" ||
			n.TeacherID != "" || n.StudentID != "" || n.ConversationID != noticeThread {
			t.Errorf("reader %d is told %+v, want the class's channel", i, n)
		}
		if n.Excerpt != string(excerpt) {
			t.Errorf("a file alone is quoted %q, want its name cut to %d characters", n.Excerpt, domain.ExcerptLength)
		}
	}
}

func TestAMessageQuotesItsBodyBeforeItsFileAndCutsAtEightyCharacters(t *testing.T) {
	body, filename := strings.Repeat("ư", domain.ExcerptLength+5), "bai-tap.pdf"
	d := domain.Delivery{ConversationID: noticeThread, Kind: domain.KindDirect, StaffID: noticeStaff, StudentID: noticeStudent,
		Body: &body, Filename: &filename, Readers: []string{noticeStudent}}
	if got := d.Notices()[0].Excerpt; got != strings.Repeat("ư", domain.ExcerptLength) {
		t.Errorf("the excerpt is %q, want the body's first %d characters", got, domain.ExcerptLength)
	}
	if got := (domain.Delivery{Readers: nil}).Notices(); len(got) != 0 {
		t.Errorf("a message nobody reads tells %+v", got)
	}
}
