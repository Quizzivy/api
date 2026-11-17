// Package ports is what the sharing context needs from outside it: the test
// or question set a grant names, the signed URL of a profile photo, and the
// notifications module's Notify.
package ports

import (
	"context"

	notificationscommand "quizzivy/internal/modules/notifications/application/command"
	"quizzivy/internal/modules/sharing/domain"
	"quizzivy/internal/shared/access"
	"quizzivy/internal/shared/cqrs"
)

// Content resolves the live test or question set kind and id name, as scope
// reads it under its own module's rules: its name and owner, or
// domain.ErrNotFound when it is missing, deleted or not readable by scope.
type Content interface {
	Resource(ctx context.Context, scope access.Scope, kind domain.Kind, id string) (domain.Resource, error)
}

// Avatars signs the URL of a profile photo from the key a user row holds, and
// answers an empty URL when there is none to show.
type Avatars interface {
	AvatarURL(ctx context.Context, key string) (string, error)
}

// Notifier is the notifications module's Notify, which tells each new
// recipient of a share (content.shared).
type Notifier = cqrs.CommandHandler[notificationscommand.Notify, cqrs.Nothing]
