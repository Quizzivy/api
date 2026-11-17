package query

import (
	"context"

	"quizzivy/internal/modules/messaging/application/internal/support"
	"quizzivy/internal/shared/access"
)

// StaffUnread counts the messages past Scope.UserID's read pointers, not
// counting their own, over their direct threads and the channels of the
// classes Scope reaches that are not archived: TeacherSummary.unreadMessages,
// which the nav reads in the caller's own scope.
type StaffUnread struct {
	Scope access.Scope
}

type StaffUnreadHandler struct {
	*support.Service
}

func (s StaffUnreadHandler) Handle(ctx context.Context, q StaffUnread) (int, error) {
	return s.Repo.StaffUnread(ctx, q.Scope)
}

// StudentUnread counts the messages past StudentID's read pointers, not
// counting their own, over their direct threads and the channels of the
// classes they are in now that are not archived: MySummary.unreadMessages,
// which is 0 for an account that is not a student.
type StudentUnread struct {
	StudentID string
}

type StudentUnreadHandler struct {
	*support.Service
}

func (s StudentUnreadHandler) Handle(ctx context.Context, q StudentUnread) (int, error) {
	return s.Repo.StudentUnread(ctx, q.StudentID)
}
