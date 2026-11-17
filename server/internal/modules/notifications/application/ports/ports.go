// Package ports is what the notifications context needs from outside it: the
// messaging context's count of a student's unread messages, which the shell's
// summary carries beside the unread notifications, and the email channel
// Notify hands each notice to.
package ports

import (
	"context"
	"time"

	messagingquery "quizzivy/internal/modules/messaging/application/query"
	"quizzivy/internal/modules/notifications/domain"
	"quizzivy/internal/shared/cqrs"
)

// Mailer enqueues the email a notice Notify wrote at now calls for, if any
// (application/email.Mailer over R5's outbox).
type Mailer interface {
	Mail(ctx context.Context, n domain.Notice, now time.Time) error
}

// UnreadMessages is the messaging module's own count of a student's unread messages, MySummary.unreadMessages.
type UnreadMessages = cqrs.QueryHandler[messagingquery.StudentUnread, int]
