// Package command is the messaging context's writes: a staff member's direct
// message, a class announcement, a student's message to a teacher, and the
// read pointers. None writes an audit row (D17).
package command

import (
	"context"

	"quizzivy/internal/modules/messaging/application/internal/support"
	"quizzivy/internal/modules/messaging/domain"
	"quizzivy/internal/shared/actor"
)

// PostDirect is a staff member's message to StudentID, text, a file or both.
// The first post makes the thread; a repeated ClientID returns the message
// the first call made. A student who is no messaging peer of the caller
// answers domain.ErrNotFound, and a peer whose account is disabled or no
// longer a student domain.ErrNotAllowed.
type PostDirect struct {
	Actor        actor.Actor
	StudentID    string
	ClientID     string
	Body         *string
	AttachmentID *string
}

type PostDirectHandler struct {
	*support.Service
}

func (s PostDirectHandler) Handle(ctx context.Context, cmd PostDirect) (domain.Posted, error) {
	content, err := domain.NewContent(cmd.Body, cmd.AttachmentID)
	if err != nil {
		return domain.Posted{}, err
	}
	posted, err := s.Repo.PostDirect(ctx, cmd.Actor.ID, cmd.StudentID, domain.Post{SenderID: cmd.Actor.ID, ClientID: cmd.ClientID, Content: content})
	if err != nil {
		return domain.Posted{}, err
	}
	s.SignMessage(ctx, cmd.Actor.ID, &posted.Message, map[string]*string{})
	return posted, nil
}
