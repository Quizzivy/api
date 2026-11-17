package query

import (
	"context"

	messagingquery "quizzivy/internal/modules/messaging/application/query"
	"quizzivy/internal/modules/notifications/application/internal/support"
	"quizzivy/internal/modules/notifications/domain"
)

// Summary counts what UserID's shell shows: their unread notifications, of
// Audience's kinds when it names one, and of every kind when it is empty. An
// Audience that is neither the teacher's nor the student's is refused. With
// Messages it also counts UserID's unread messages as a student, 0 for staff
// and without the messaging module.
type Summary struct {
	UserID   string
	Audience domain.Audience
	Messages bool
}

type SummaryHandler struct {
	*support.Service
}

func (s SummaryHandler) Handle(ctx context.Context, q Summary) (domain.Summary, error) {
	switch {
	case q.UserID == "":
		return domain.Summary{}, domain.ErrNoRecipient
	case !q.Audience.Valid():
		return domain.Summary{}, domain.ErrUnknownAudience
	}
	unread, err := s.Repo.Unread(ctx, q.UserID, q.Audience)
	if err != nil {
		return domain.Summary{}, err
	}
	out := domain.Summary{UnreadNotifications: unread}
	if q.Messages && s.Messages != nil {
		out.UnreadMessages, err = s.Messages.Handle(ctx, messagingquery.StudentUnread{StudentID: q.UserID})
	}
	return out, err
}
