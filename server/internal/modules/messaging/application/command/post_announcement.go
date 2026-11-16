package command

import (
	"context"

	"quizzivy/internal/modules/messaging/application/internal/support"
	"quizzivy/internal/modules/messaging/domain"
	"quizzivy/internal/shared/actor"
)

// PostAnnouncement is a post to the channel of ClassID, which reaches every
// active student of the class. MayWrite is whether the caller holds
// teaching.classes.write. A class out of the caller's scope answers
// domain.ErrNotFound and an archived one domain.ErrClassArchived.
type PostAnnouncement struct {
	Actor        actor.Actor
	MayWrite     bool
	ClassID      string
	ClientID     string
	Body         *string
	AttachmentID *string
}

type PostAnnouncementHandler struct {
	*support.Service
}

func (s PostAnnouncementHandler) Handle(ctx context.Context, cmd PostAnnouncement) (domain.Posted, error) {
	content, err := domain.NewContent(cmd.Body, cmd.AttachmentID)
	if err != nil {
		return domain.Posted{}, err
	}
	posted, err := s.Repo.PostChannel(ctx, cmd.Actor.Scope, cmd.ClassID, cmd.MayWrite,
		domain.Post{SenderID: cmd.Actor.ID, ClientID: cmd.ClientID, Content: content})
	if err != nil {
		return domain.Posted{}, err
	}
	signed := map[string]*string{}
	s.SignMessage(ctx, cmd.Actor.ID, &posted.Message, signed)
	return posted, nil
}
