package ports

import (
	notificationscommand "quizzivy/internal/modules/notifications/application/command"
	"quizzivy/internal/shared/cqrs"
)

// Notifier is the notifications module's Notify, which tells a test's owner
// that a co-editor published it (test.published_by_collaborator).
type Notifier = cqrs.CommandHandler[notificationscommand.Notify, cqrs.Nothing]
