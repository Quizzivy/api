package domain_test

import (
	"strings"
	"testing"
	"unicode/utf8"

	"quizzivy/internal/modules/messaging/domain"
)

func TestAnAttachmentsFilenameIsSanitisedAndEndsInItsType(t *testing.T) {
	long := strings.Repeat("ộ", 250)
	cases := []struct {
		name, mime, want string
	}{
		{"Bài tập tuần 3.pdf", domain.MimePDF, "Bài tập tuần 3.pdf"},
		{"  ghi-am.M4A ", domain.MimeM4A, "ghi-am.M4A"},
		{"ghi-am.mp4", domain.MimeM4A, "ghi-am.mp4"},
		{"anh.jpeg", domain.MimeJPEG, "anh.jpeg"},
		{"anh.JPG", domain.MimeJPEG, "anh.JPG"},
		{"phieu-on-tap", domain.MimePDF, "phieu-on-tap.pdf"},
		{"de-thi.exe", domain.MimePDF, "de-thi.exe.pdf"},
		{"de.docx", domain.MimeDOC, "de.docx.doc"},
		{`C:\Users\co\Đề.docx`, domain.MimeDOCX, "Đề.docx"},
		{"../../etc/passwd", domain.MimePDF, "passwd.pdf"},
		{"", domain.MimePDF, "tệp-không-tên.pdf"},
		{".pdf", domain.MimePDF, ".pdf.pdf"},
		{"/", domain.MimePNG, "tệp-không-tên.png"},
		// A name written decomposed is stored composed.
		{"e\u0301.pdf", domain.MimePDF, "\u00e9.pdf"},
	}
	for _, c := range cases {
		if got := domain.AttachmentFilename(c.name, c.mime); got != c.want {
			t.Errorf("AttachmentFilename(%q, %s) = %q, want %q", c.name, c.mime, got, c.want)
		}
	}
	for _, name := range []string{long, long + ".pdf"} {
		got := domain.AttachmentFilename(name, domain.MimePDF)
		if utf8.RuneCountInString(got) != domain.MaxFilename || !strings.HasSuffix(got, ".pdf") {
			t.Errorf("a %d-character name became %d characters (%q...), want %d ending in .pdf", utf8.RuneCountInString(name), utf8.RuneCountInString(got), got[:12], domain.MaxFilename)
		}
	}
	if err := domain.CheckFilename("a\x00b.pdf"); err != domain.ErrInvalidFilename {
		t.Errorf("a name holding U+0000: %v, want ErrInvalidFilename", err)
	}
}

func TestAFileIsStoredUnderMessagesByItsIdAndType(t *testing.T) {
	for mime, want := range map[string]string{
		domain.MimePDF: ".pdf", domain.MimeDOCX: ".docx", domain.MimeDOC: ".doc", domain.MimePNG: ".png",
		domain.MimeJPEG: ".jpg", domain.MimeMP3: ".mp3", domain.MimeM4A: ".m4a", "image/webp": "",
	} {
		if got := domain.StorageKey("0193", mime); got != "messages/0193"+want {
			t.Errorf("StorageKey(%s) = %q, want messages/0193%s", mime, got, want)
		}
	}
}

func TestTheDownloadRuleLetsInTheThreadOrTheUploaderBeforeSending(t *testing.T) {
	rules := domain.Eligibility{}
	cases := []struct {
		name  string
		facts domain.AttachmentFacts
		ok    bool
	}{
		{"the uploader of an unsent file", domain.AttachmentFacts{Uploader: true}, true},
		{"anyone else on an unsent file", domain.AttachmentFacts{}, false},
		{"a participant of the thread that holds it", domain.AttachmentFacts{Sent: true, Participant: true}, true},
		{"the uploader once it is sent, outside the thread", domain.AttachmentFacts{Sent: true, Uploader: true}, false},
		{"anyone else on a sent file", domain.AttachmentFacts{Sent: true}, false},
	}
	for _, c := range cases {
		err := rules.Download(c.facts)
		if (err == nil) != c.ok || (err != nil && err != domain.ErrAttachmentNotFound) {
			t.Errorf("%s: %v, want allowed %v (or ErrAttachmentNotFound)", c.name, err, c.ok)
		}
	}
}
