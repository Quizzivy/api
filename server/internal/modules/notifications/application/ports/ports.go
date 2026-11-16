// Package ports is what the notifications context needs from outside it: the
// messaging context's count of a student's unread messages, which the shell's
// summary carries beside the unread notifications.
package ports

import (
	messagingquery "quizzivy/internal/modules/messaging/application/query"
	"quizzivy/internal/shared/cqrs"
)

// UnreadMessages is the messaging module's own count of a student's unread messages, MySummary.unreadMessages.
type UnreadMessages = cqrs.QueryHandler[messagingquery.StudentUnread, int]
