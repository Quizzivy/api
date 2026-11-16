package command

import (
	"context"

	"quizzivy/internal/modules/messaging/application/internal/support"
	"quizzivy/internal/modules/messaging/domain"
	"quizzivy/internal/shared/cqrs"
)

// MarkReadAsStudent moves StudentID's read pointer forward to MessageID in
// their thread with TeacherID, or, with ClassID, in that class's channel. A
// thread the student cannot read answers domain.ErrNotFound, and a message
// that is not one of its domain.ErrNotInThread. A pointer that leaves
// nothing unread in the thread clears the student's in-app item for it.
type MarkReadAsStudent struct {
	StudentID string
	TeacherID string
	ClassID   string
	MessageID string
}

type MarkReadAsStudentHandler struct {
	*support.Service
}

func (s MarkReadAsStudentHandler) Handle(ctx context.Context, cmd MarkReadAsStudent) (cqrs.Nothing, error) {
	mark := domain.ReadMark{ReaderID: cmd.StudentID, MessageID: cmd.MessageID}
	if cmd.ClassID != "" {
		if _, err := s.Repo.StudentChannel(ctx, cmd.StudentID, cmd.ClassID); err != nil {
			return cqrs.Nothing{}, err
		}
		return cqrs.Nothing{}, s.MoveRead(ctx, mark.ReaderID, func() (domain.ReadState, error) { return s.Repo.MarkChannelRead(ctx, cmd.ClassID, mark) })
	}
	if _, err := s.AsStudent(ctx, cmd.StudentID, cmd.TeacherID); err != nil {
		return cqrs.Nothing{}, err
	}
	return cqrs.Nothing{}, s.MoveRead(ctx, mark.ReaderID, func() (domain.ReadState, error) {
		return s.Repo.MarkDirectRead(ctx, cmd.TeacherID, cmd.StudentID, mark)
	})
}
