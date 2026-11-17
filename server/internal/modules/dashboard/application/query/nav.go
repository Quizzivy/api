package query

import (
	"context"
	"quizzivy/internal/modules/dashboard/application/internal/support"
	"quizzivy/internal/modules/dashboard/domain"
	messagingquery "quizzivy/internal/modules/messaging/application/query"
	notificationsquery "quizzivy/internal/modules/notifications/application/query"
	notificationsdomain "quizzivy/internal/modules/notifications/domain"
	sharingquery "quizzivy/internal/modules/sharing/application/query"
	"quizzivy/internal/shared/access"
)

// Nav reads only the shell counts the caller may see, among them the live
// grants to the caller (Shared with me) and the caller's unread messages in
// Scope (Messages). Audience narrows the unread
// notifications to one console's kinds and leaves the other counts as they
// are; the empty Audience counts every kind.
type Nav struct {
	Scope    access.Scope
	CanGrade bool
	Audience notificationsdomain.Audience
}

type NavHandler struct{ *support.Service }

func (s NavHandler) Handle(ctx context.Context, q Nav) (domain.Nav, error) {
	if s.Notifications == nil {
		return domain.Nav{}, domain.ErrNotificationsUnavailable
	}
	live, err := s.Repo.LiveAssignments(ctx, q.Scope, s.Now())
	if err != nil {
		return domain.Nav{}, err
	}
	out := domain.Nav{LiveAssignments: &live}
	if q.CanGrade {
		count, err := s.Repo.AnswersToGrade(ctx, q.Scope)
		if err != nil {
			return domain.Nav{}, err
		}
		out.AnswersToGrade = &count
	}
	if s.SharedWithMe != nil {
		shared, err := s.SharedWithMe.Handle(ctx, sharingquery.CountSharedWithMe{UserID: q.Scope.UserID})
		if err != nil {
			return domain.Nav{}, err
		}
		out.SharedWithMe = shared
	}
	if s.Messages != nil {
		unread, err := s.Messages.Handle(ctx, messagingquery.StaffUnread{Scope: q.Scope})
		if err != nil {
			return domain.Nav{}, err
		}
		out.UnreadMessages = unread
	}
	notifications, err := s.Notifications.Handle(ctx, notificationsquery.Summary{UserID: q.Scope.UserID, Audience: q.Audience})
	out.UnreadNotifications = notifications.UnreadNotifications
	return out, err
}
