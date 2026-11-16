// Package ports is what the messaging context needs from outside it: the
// signed URL of a profile photo, for the students and staff its threads name,
// the flagged attempts a staff member's direct thread shows beside its
// messages, the object store that holds messages' files, and the audio probe
// that tells an MP3 or an M4A.
package ports

import (
	"context"
	"io"
	"time"

	"quizzivy/internal/modules/messaging/domain"
	"quizzivy/internal/shared/access"
)

// Avatars signs the URL of a profile photo from the key a user row holds, and
// answers an empty URL when there is none to show.
type Avatars interface {
	AvatarURL(ctx context.Context, key string) (string, error)
}

// ContextEvents reads the attempts of studentID that were flagged on an
// assignment scope reaches, among the papers scope sees, from since on,
// oldest first: the attempts context's query, so the messaging context never
// reads attempts itself.
type ContextEvents interface {
	FlaggedAttempts(ctx context.Context, scope access.Scope, studentID string, since time.Time) ([]domain.ContextEvent, error)
}

// AttachmentStore keeps messages' files under domain.AttachmentPrefix in the
// private bucket.
type AttachmentStore interface {
	Put(ctx context.Context, key, contentType string, body io.Reader, size int64) error
	Delete(ctx context.Context, key string) error
	// SignedAttachmentURL mints a GET valid for ttl that downloads the
	// object as an attachment named filename, with contentType as its type.
	SignedAttachmentURL(ctx context.Context, key, filename, contentType string, ttl time.Duration) (string, error)
	// ListAfter returns at most limit objects under prefix whose keys sort
	// after after, in key order.
	ListAfter(ctx context.Context, prefix, after string, limit int) ([]StoredObject, error)
}

// StoredObject is an object's key and when it was written.
type StoredObject struct {
	Key       string
	WrittenAt time.Time
}

// AudioProbe tells an MP3 or an M4A by its content and reads its duration,
// failing with domain.ErrUnsupportedType for anything else, a file whose
// duration cannot be read included.
type AudioProbe interface {
	Audio(r io.ReaderAt, size int64) (mime string, err error)
}
