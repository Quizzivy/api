// Package command is the messaging context's writes: a staff member's direct
// message, a class announcement, a message to a colleague, a student's
// message to a teacher, and the read pointers. None writes an audit row
// (D17).
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
	return post(ctx, s.Service, cmd.Actor.ID, cmd.ClientID, cmd.Body, cmd.AttachmentID, func(p domain.Post) (domain.Posted, error) {
		return s.Repo.PostDirect(ctx, cmd.Actor.ID, cmd.StudentID, p)
	})
}

// post is a staff member's message to a pair's thread: it makes the content
// (domain.NewContent's refusals), writes it through write, tells the
// thread's readers once it has committed, and signs the message for its
// sender.
func post(ctx context.Context, s *support.Service, senderID, clientID string, body, attachmentID *string,
	write func(domain.Post) (domain.Posted, error)) (domain.Posted, error) {
	content, err := domain.NewContent(body, attachmentID)
	if err != nil {
		return domain.Posted{}, err
	}
	posted, err := write(domain.Post{SenderID: senderID, ClientID: clientID, Content: content})
	if err != nil {
		return domain.Posted{}, err
	}
	s.Ring(ctx, posted)
	s.SignMessage(ctx, senderID, &posted.Message, map[string]*string{})
	return posted, nil
}
