package adapters

import (
	"context"
	"errors"
	"io"
	"time"

	attemptsquery "quizzivy/internal/modules/attempts/application/query"
	attemptsdomain "quizzivy/internal/modules/attempts/domain"
	identityquery "quizzivy/internal/modules/identity/application/query"
	messagingports "quizzivy/internal/modules/messaging/application/ports"
	messagingdomain "quizzivy/internal/modules/messaging/domain"
	"quizzivy/internal/platform/probe"
	"quizzivy/internal/platform/storage"
	"quizzivy/internal/shared/access"
	"quizzivy/internal/shared/cqrs"
)

// MessagingPhotos is identity's AvatarURL query behind the messaging module's
// Avatars port: it signs the photo of a thread's student and of each
// message's sender, staff or student alike, and answers an empty URL when the
// account has none or no store is configured.
type MessagingPhotos struct {
	Query cqrs.QueryHandler[identityquery.AvatarURL, string]
}

// AvatarURL signs a GET for the stored photo key.
func (a MessagingPhotos) AvatarURL(ctx context.Context, key string) (string, error) {
	return a.Query.Handle(ctx, identityquery.AvatarURL{Key: &key})
}

// MessagingContextEvents is the attempts module's FlaggedAttempts query
// behind the messaging module's ContextEvents port: a staff member's direct
// thread shows the student's flagged attempts on the assignments the staff
// member's scope reaches, among the papers that scope sees, without the
// messaging module reading attempts.
type MessagingContextEvents struct {
	Query cqrs.QueryHandler[attemptsquery.FlaggedAttempts, []attemptsdomain.FlaggedAttempt]
}

// FlaggedAttempts reads studentID's flagged attempts from since on, oldest
// first.
func (a MessagingContextEvents) FlaggedAttempts(ctx context.Context, scope access.Scope, studentID string, since time.Time) ([]messagingdomain.ContextEvent, error) {
	flagged, err := a.Query.Handle(ctx, attemptsquery.FlaggedAttempts{Scope: scope, StudentID: studentID, Since: since})
	if err != nil {
		return nil, err
	}
	out := make([]messagingdomain.ContextEvent, len(flagged))
	for i, f := range flagged {
		out[i] = messagingdomain.ContextEvent{AttemptID: f.AttemptID, AssignmentID: f.AssignmentID, Title: f.Title, FocusLost: f.FocusLost, At: f.At}
	}
	return out, nil
}

// MessagingAttachments is the object store behind the messaging module's
// AttachmentStore port: the media bucket, under messages/.
type MessagingAttachments struct {
	Objects *storage.Client
}

func (a MessagingAttachments) Put(ctx context.Context, key, contentType string, body io.Reader, size int64) error {
	return a.Objects.Put(ctx, key, contentType, body, size)
}

func (a MessagingAttachments) Delete(ctx context.Context, key string) error {
	return a.Objects.Delete(ctx, key)
}

// SignedAttachmentURL signs a GET that downloads the object as an attachment
// named filename, of type contentType.
func (a MessagingAttachments) SignedAttachmentURL(ctx context.Context, key, filename, contentType string, ttl time.Duration) (string, error) {
	return a.Objects.SignedAttachmentURL(ctx, key, filename, contentType, ttl)
}

// ListAfter lists the objects under prefix past after, in key order.
func (a MessagingAttachments) ListAfter(ctx context.Context, prefix, after string, limit int) ([]messagingports.StoredObject, error) {
	objects, err := a.Objects.ListAfter(ctx, prefix, after, limit)
	if err != nil {
		return nil, err
	}
	out := make([]messagingports.StoredObject, len(objects))
	for i, o := range objects {
		out[i] = messagingports.StoredObject{Key: o.Key, WrittenAt: o.LastModified}
	}
	return out, nil
}

// MessagingAudio is the platform probe behind the messaging module's
// AudioProbe port: an MP3 or an M4A whose duration it reads, and
// ErrUnsupportedType for anything else.
type MessagingAudio struct{}

func (MessagingAudio) Audio(r io.ReaderAt, size int64) (string, error) {
	mime, _, err := probe.Audio(r, size)
	if errors.Is(err, probe.ErrUnsupportedType) || errors.Is(err, probe.ErrUnmeasurable) {
		return "", errors.Join(messagingdomain.ErrUnsupportedType, err)
	}
	return mime, err
}
