// Package ports is what the messaging context needs from outside it: the
// signed URL of a profile photo, for the students and staff its threads name,
// and the flagged attempts a staff member's direct thread shows beside its
// messages.
package ports

import (
	"context"
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
// assignment scope reaches, from since on, oldest first: the attempts
// context's query, so the messaging context never reads attempts itself.
type ContextEvents interface {
	FlaggedAttempts(ctx context.Context, scope access.Scope, studentID string, since time.Time) ([]domain.ContextEvent, error)
}
