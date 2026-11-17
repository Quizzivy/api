// Package filetype tells what an uploaded file is from its content, never from
// its name or the Content-Type its uploader sent: both come from the uploader,
// and the question is what the file is (§11.1). It holds the magic bytes of
// the images and PDFs the API accepts, the image header's size, and the two
// Word formats a message attachment may be (T-R7.11).
//
// It reads with the standard library only and decodes nothing beyond a
// header: an image's pixels, a PDF and a Word document are never opened.
package filetype

import (
	"bytes"
	"errors"
	"image/jpeg"
	"image/png"
	"io"
)

// The MIME types this package names.
const (
	PNG  = "image/png"
	JPEG = "image/jpeg"
	WebP = "image/webp"
	PDF  = "application/pdf"
	DOCX = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	DOC  = "application/msword"
)

// HeadSize is how many leading bytes Image and Sniff need to tell every type
// they know.
const HeadSize = 16

// Image is §11.1's image allowlist, matched on magic bytes: PNG, JPEG or
// WebP, or "" for anything else.
func Image(head []byte) string {
	switch {
	case len(head) >= 8 && string(head[0:8]) == "\x89PNG\r\n\x1a\n":
		return PNG
	case len(head) >= 3 && head[0] == 0xFF && head[1] == 0xD8 && head[2] == 0xFF:
		return JPEG
	case len(head) >= 12 && string(head[0:4]) == "RIFF" && string(head[8:12]) == "WEBP":
		return WebP
	}
	return ""
}

// Sniff is Image, and PDF for a file that begins with "%PDF-" at offset 0.
// It answers "" for anything else, a Word file included: those need the whole
// file (Word).
func Sniff(head []byte) string {
	if mime := Image(head); mime != "" {
		return mime
	}
	if bytes.HasPrefix(head, []byte("%PDF-")) {
		return PDF
	}
	return ""
}

// ErrDimensions is an image whose header declares more pixels a side than
// the caller allows.
var ErrDimensions = errors.New("filetype: image dimensions over the limit")

// ErrUnreadable is an image whose header cannot be read.
var ErrUnreadable = errors.New("filetype: unreadable image header")

// Dimensions reads the pixel size a PNG or JPEG header declares, through the
// standard library's DecodeConfig, which reads the header and decodes no
// pixel. It answers ErrDimensions when either side is over maxSide, and
// ErrUnreadable for a header that cannot be read, names no positive size, or
// a mime that is neither type.
func Dimensions(r io.Reader, mime string, maxSide int) (width, height int, err error) {
	decode := map[string]func(io.Reader) (int, int, error){
		PNG: func(r io.Reader) (int, int, error) {
			cfg, err := png.DecodeConfig(r)
			return cfg.Width, cfg.Height, err
		},
		JPEG: func(r io.Reader) (int, int, error) {
			cfg, err := jpeg.DecodeConfig(r)
			return cfg.Width, cfg.Height, err
		},
	}[mime]
	if decode == nil {
		return 0, 0, ErrUnreadable
	}
	width, height, err = decode(r)
	if err != nil || width <= 0 || height <= 0 {
		return 0, 0, errors.Join(ErrUnreadable, err)
	}
	if width > maxSide || height > maxSide {
		return width, height, ErrDimensions
	}
	return width, height, nil
}
