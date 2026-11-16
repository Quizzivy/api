package adapters

import (
	"context"
	"time"

	attemptsquery "quizzivy/internal/modules/attempts/application/query"
	attemptsdomain "quizzivy/internal/modules/attempts/domain"
	identityquery "quizzivy/internal/modules/identity/application/query"
	messagingdomain "quizzivy/internal/modules/messaging/domain"
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
// member's scope reaches, without the messaging module reading attempts.
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
