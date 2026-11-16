package domain_test

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"quizzivy/internal/modules/messaging/domain"
)

func TestAPostKeepsATrimmedBodyAndNeedsTextOrAFile(t *testing.T) {
	text := func(s string) *string { return &s }
	for name, c := range map[string]struct {
		body, attachment *string
		want             *string
		err              error
	}{
		"text":                    {body: text("  Chào em\n"), want: text("Chào em")},
		"a file alone":            {attachment: text("019535d9-3df7-79fb-b466-fa907fa17f9e")},
		"a blank body and a file": {body: text(" \t\n"), attachment: text("019535d9-3df7-79fb-b466-fa907fa17f9e")},
		"a blank body":            {body: text(" \r\n\t "), err: domain.ErrEmpty},
		"nothing":                 {err: domain.ErrEmpty},
		"4000 characters":         {body: text(" " + strings.Repeat("ữ", domain.MaxBody) + " "), want: text(strings.Repeat("ữ", domain.MaxBody))},
		"4001 characters":         {body: text(strings.Repeat("a", domain.MaxBody+1)), err: domain.ErrInvalid},
	} {
		got, err := domain.NewContent(c.body, c.attachment)
		if !errors.Is(err, c.err) {
			t.Errorf("%s: err = %v, want %v", name, err, c.err)
			continue
		}
		if c.err != nil {
			continue
		}
		if (got.Body == nil) != (c.want == nil) || (got.Body != nil && *got.Body != *c.want) {
			t.Errorf("%s: body = %v, want %v", name, got.Body, c.want)
		}
	}
}

func TestACursorNamesOneEndAndAPageOfAtMostFifty(t *testing.T) {
	if _, err := (domain.Cursor{Before: "a", After: "b"}).Normalize(); !errors.Is(err, domain.ErrInvalid) {
		t.Errorf("before and after: %v, want ErrInvalid", err)
	}
	for limit, want := range map[int]int{0: domain.DefaultPage, -3: domain.DefaultPage, 1: 1, 50: 50, 51: domain.MaxPage} {
		if got, err := (domain.Cursor{Limit: limit}).Normalize(); err != nil || got.Limit != want {
			t.Errorf("limit %d: %d, %v, want %d", limit, got.Limit, err, want)
		}
	}
}

func TestAPreviewIsTheBodyOnOneLineCutToItsLength(t *testing.T) {
	body := "Lịch học\n\ntuần   sau " + strings.Repeat("ơ", 200)
	p := domain.PreviewOf(&body, nil, true)
	if p.Text == nil || utf8.RuneCountInString(*p.Text) != domain.PreviewLength || !strings.HasPrefix(*p.Text, "Lịch học tuần sau ơ") || !p.Mine {
		t.Errorf("preview = %+v", p)
	}
	file := "de-cuong.pdf"
	if p := domain.PreviewOf(nil, &file, false); p.Text != nil || p.AttachmentName == nil || *p.AttachmentName != file {
		t.Errorf("a file's preview = %+v, want no text and its name", p)
	}
}
