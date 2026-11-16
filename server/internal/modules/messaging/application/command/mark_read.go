package command

import (
	"context"

	"quizzivy/internal/modules/messaging/application/internal/support"
	"quizzivy/internal/modules/messaging/domain"
	"quizzivy/internal/shared/actor"
	"quizzivy/internal/shared/cqrs"
)

// MarkRead moves the caller's read pointer in the thread Thread names forward
// to MessageID. A thread the caller cannot read answers domain.ErrNotFound,
// and a message that is not one of its domain.ErrNotInThread; an older
// message than the pointer's changes nothing. A pointer that leaves nothing
// unread in the thread clears the caller's in-app item for it.
type MarkRead struct {
	Actor     actor.Actor
	Thread    domain.ThreadRef
	MessageID string
}

type MarkReadHandler struct {
	*support.Service
}

func (s MarkReadHandler) Handle(ctx context.Context, cmd MarkRead) (cqrs.Nothing, error) {
	mark := domain.ReadMark{ReaderID: cmd.Actor.ID, MessageID: cmd.MessageID}
	switch cmd.Thread.Kind {
	case domain.KindDirect:
		if _, err := s.Direct(ctx, cmd.Actor.ID, cmd.Thread.StudentID); err != nil {
			return cqrs.Nothing{}, err
		}
		return cqrs.Nothing{}, s.MoveRead(ctx, mark.ReaderID, func() (domain.ReadState, error) {
			return s.Repo.MarkDirectRead(ctx, cmd.Actor.ID, cmd.Thread.StudentID, mark)
		})
	case domain.KindClass:
		if _, err := s.Repo.Channel(ctx, cmd.Actor.Scope, cmd.Thread.ClassID); err != nil {
			return cqrs.Nothing{}, err
		}
		return cqrs.Nothing{}, s.MoveRead(ctx, mark.ReaderID, func() (domain.ReadState, error) { return s.Repo.MarkChannelRead(ctx, cmd.Thread.ClassID, mark) })
	}
	return cqrs.Nothing{}, domain.ErrNotFound
}
