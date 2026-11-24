package domain

import (
	"errors"
	"path"
	"strings"
	"time"

	"quizzivy/internal/shared/content"
)

// The bounds of a message's file (T-R7.11, D17).
const (
	// MaxAttachmentBytes is the largest file a staff member may attach.
	MaxAttachmentBytes = 20 << 20
	// MaxImageSide is the most pixels a side an attached image's header may
	// declare.
	MaxImageSide = 10000
	// AttachmentURLTTL is how long a download URL is valid.
	AttachmentURLTTL = 5 * time.Minute
	// UnsentGrace is how long an uploaded file may wait for a post before the
	// daily housekeeping removes it, and how old an object under
	// AttachmentPrefix must be before the sweep may remove it unnamed.
	UnsentGrace = 24 * time.Hour
	// AttachmentPrefix is where every message's file is stored.
	AttachmentPrefix = "messages/"
	// MaxFilename is the most characters a stored filename keeps.
	MaxFilename = 200
)

var (
	// ErrStorageOff is an upload or a download with no object store
	// configured.
	ErrStorageOff = errors.New("messaging: no object store for attachments")
	// ErrTooLarge is a file over MaxAttachmentBytes (MEDIA_TOO_LARGE).
	ErrTooLarge = errors.New("messaging: attachment too large")
	// ErrUnsupportedType is a file of a type an attachment may not be, a
	// WebP and an empty file included (MEDIA_TYPE_UNSUPPORTED).
	ErrUnsupportedType = errors.New("messaging: unsupported attachment type")
	// ErrImageDimensions is an image whose header declares more than
	// MaxImageSide pixels a side (IMAGE_DIMENSIONS).
	ErrImageDimensions = errors.New("messaging: image dimensions over the limit")
	// ErrHasMacros is a Word file that carries macros (ATTACHMENT_HAS_MACROS).
	ErrHasMacros = errors.New("messaging: the document carries macros")
	// ErrInvalidFilename is a filename that cannot be stored: one holding
	// U+0000.
	ErrInvalidFilename = errors.New("messaging: invalid filename")
)

// The types a message's file may be, as app.message_attachments allows them.
const (
	MimePDF  = "application/pdf"
	MimeDOCX = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	MimeDOC  = "application/msword"
	MimePNG  = "image/png"
	MimeJPEG = "image/jpeg"
	MimeMP3  = "audio/mpeg"
	MimeM4A  = "audio/mp4"
)

// extensions are each type's stored extension first, then the others a
// filename may already end in for it.
var extensions = map[string][]string{
	MimePDF:  {".pdf"},
	MimeDOCX: {".docx"},
	MimeDOC:  {".doc"},
	MimePNG:  {".png"},
	MimeJPEG: {".jpg", ".jpeg"},
	MimeMP3:  {".mp3"},
	MimeM4A:  {".m4a", ".mp4"},
}

// Extension is the extension a file of mime is stored under, or "" for a
// type an attachment may not be.
func Extension(mime string) string {
	if exts := extensions[mime]; len(exts) > 0 {
		return exts[0]
	}
	return ""
}

// StorageKey is where the file id of type mime is stored:
// messages/{id}.{ext}.
func StorageKey(id, mime string) string {
	return AttachmentPrefix + id + Extension(mime)
}

// CheckFilename refuses a filename AttachmentFilename cannot make storable:
// one holding U+0000.
func CheckFilename(name string) error {
	if strings.ContainsRune(name, 0) {
		return ErrInvalidFilename
	}
	return nil
}

// AttachmentFilename is the name a file of type mime is stored and downloaded
// under. It is sanitised as the media library's are (composed, any path
// before it dropped, trimmed, a blank one named "tệp-không-tên", at most
// MaxFilename characters), and it then ends in an extension of the type its
// content sniffed as: "report" becomes "report.pdf", and "bai-tap.exe" that
// holds a PDF "bai-tap.exe.pdf", so the file a student saves opens as what it
// is.
func AttachmentFilename(name, mime string) string {
	name = path.Base(strings.ReplaceAll(strings.TrimSpace(content.NFC(name)), "\\", "/"))
	base, ext := strings.TrimSpace(name), Extension(mime)
	for _, known := range extensions[mime] {
		if len(base) > len(known) && strings.EqualFold(base[len(base)-len(known):], known) {
			base, ext = base[:len(base)-len(known)], base[len(base)-len(known):]
			break
		}
	}
	if base == "" || base == "." || base == "/" {
		base = "tệp-không-tên"
	}
	if runes, keep := []rune(base), MaxFilename-len(ext); len(runes) > keep {
		base = string(runes[:keep])
	}
	return base + ext
}

// NewAttachment is a file the upload stored, the row it makes.
type NewAttachment struct {
	ID         string
	UploadedBy string
	StorageKey string
	MimeType   string
	Bytes      int64
	Filename   string
	Checksum   []byte
}

// StoredAttachment is a file as the download reads it: where it is stored,
// who uploaded it, and the thread of the message that holds it, nil while
// none does.
type StoredAttachment struct {
	Attachment
	StorageKey string
	UploadedBy string
	Thread     *AttachmentThread
}

// AttachmentThread is the thread of the message that holds a file: a direct
// thread's two participants, a colleague thread's pair, or a channel's class.
type AttachmentThread struct {
	Kind            Kind
	StaffID         string
	StudentID       string
	ColleagueLowID  string
	ColleagueHighID string
	ClassID         string
}

// InPair reports whether reader is one of a colleague thread's two staff
// members, the only readers of its files: never true for another kind.
func (t AttachmentThread) InPair(reader string) bool {
	return t.Kind == KindColleague && reader != "" && (reader == t.ColleagueLowID || reader == t.ColleagueHighID)
}

// AttachmentFacts is what decides whether a reader may download a file.
type AttachmentFacts struct {
	// Uploader is the reader having uploaded it.
	Uploader bool
	// Sent is a message holding it.
	Sent bool
	// Participant is the reader taking part in the thread of the message
	// that holds it: a direct thread's staff member or student, a colleague
	// thread's two staff members, or a reader of a class channel (class
	// scope, or a current member).
	Participant bool
}

// Download lets the participants of the thread whose message holds the file
// fetch it, and its uploader before it is sent: ErrAttachmentNotFound for
// anyone else, an admin outside the thread included, the same answer as a
// missing id.
func (Eligibility) Download(f AttachmentFacts) error {
	if f.Sent && f.Participant || !f.Sent && f.Uploader {
		return nil
	}
	return ErrAttachmentNotFound
}

// AttachmentURL is a file's download URL and when it stops working.
type AttachmentURL struct {
	URL       string
	ExpiresAt time.Time
}
