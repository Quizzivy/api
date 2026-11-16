package domain

import (
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

// The bounds of a message and of a page of a thread.
const (
	MaxBody     = 4000
	DefaultPage = 30
	MaxPage     = 50
)

var (
	// ErrEmpty is a post with neither a body nor a file (VALIDATION_FAILED).
	ErrEmpty = errors.New("messaging: a message needs a body or a file")
	// ErrInvalid is a request the thread cannot answer: a body past MaxBody
	// characters, or a page asked both before and after a message.
	ErrInvalid = errors.New("messaging: invalid request")
	// ErrNotInThread is a read pointer naming a message of another thread, or
	// none (VALIDATION_FAILED).
	ErrNotInThread = errors.New("messaging: the message is not in this thread")
	// ErrClientIDTaken is a clientId the sender already used for a message in
	// another thread (VALIDATION_FAILED): a retry names the thread it was for.
	ErrClientIDTaken = errors.New("messaging: the clientId names a message of another thread")
	// ErrAttachmentNotFound is a file that is not the sender's to send: missing,
	// someone else's, or already sent.
	ErrAttachmentNotFound = errors.New("messaging: attachment not found")
)

// Content is what a post carries: a trimmed body, a file, or both.
type Content struct {
	Body         *string
	AttachmentID string
}

// NewContent trims body, an empty one being none, and refuses a post with
// neither a body nor a file (ErrEmpty) or a body past MaxBody characters
// (ErrInvalid). What it keeps passes messages_body_check.
func NewContent(body, attachmentID *string) (Content, error) {
	var c Content
	if body != nil {
		if trimmed := strings.TrimSpace(*body); trimmed != "" {
			if utf8.RuneCountInString(trimmed) > MaxBody {
				return Content{}, ErrInvalid
			}
			c.Body = &trimmed
		}
	}
	if attachmentID != nil {
		c.AttachmentID = *attachmentID
	}
	if c.Body == nil && c.AttachmentID == "" {
		return Content{}, ErrEmpty
	}
	return c, nil
}

// Attachment is a file a message carries.
type Attachment struct {
	ID       string
	Filename string
	Bytes    int
	MimeType string
}

// SeenBy is an announcement's "Seen by {Seen} of {Of}": Of counts the class's
// active students now, and Seen those whose read pointer has reached the post.
type SeenBy struct {
	Seen int
	Of   int
}

// Message is a message as staff read it. SenderAvatarKey is the stored key of
// the sender's photo, which the application signs into SenderAvatarURL.
type Message struct {
	ID              string
	ConversationID  string
	SenderID        string
	SenderName      string
	SenderAvatarKey *string
	SenderAvatarURL *string
	Side            Side
	Body            *string
	Attachment      *Attachment
	CreatedAt       time.Time
	Mine            bool
	SeenBy          *SeenBy
}

// Posted is a post's outcome: the message, and whether this call made it or
// its clientId repeated one already made.
type Posted struct {
	Message Message
	Created bool
}

// Post is a message a sender writes to a thread, ClientID making a retry
// return the message the first call made.
type Post struct {
	SenderID string
	ClientID string
	Content  Content
}

// Cursor is which page of a thread to read: the newest Limit messages, those
// older than Before, or those newer than After (an open thread's poll), never
// both.
type Cursor struct {
	Before string
	After  string
	Limit  int
}

// Normalize clamps Limit to 1..MaxPage, DefaultPage when unset, and refuses a
// cursor that names both ends (ErrInvalid).
func (c Cursor) Normalize() (Cursor, error) {
	if c.Before != "" && c.After != "" {
		return Cursor{}, ErrInvalid
	}
	if c.Limit < 1 {
		c.Limit = DefaultPage
	}
	if c.Limit > MaxPage {
		c.Limit = MaxPage
	}
	return c, nil
}

// Page is one page of a thread, oldest first. NextBefore names the oldest
// message of the page while older ones exist, for the next Before; LatestID
// is the thread's newest message, whatever the page.
type Page struct {
	Messages   []Message
	NextBefore *string
	LatestID   *string
}

// ReadMark moves Reader's pointer in a thread to MessageID, which must be a
// message of that thread. The pointer only moves forward.
type ReadMark struct {
	ReaderID  string
	MessageID string
}
