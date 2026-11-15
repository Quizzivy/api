package command

import (
	"context"

	"quizzivy/internal/modules/leads/application/internal/support"
	"quizzivy/internal/modules/leads/domain"
	"quizzivy/internal/shared/actor"
	"quizzivy/internal/shared/cqrs"
)

// Delete removes the request ID for good, for Actor, as when the person asks
// the centre to remove their details. A missing request fails with
// domain.ErrNotFound.
type Delete struct {
	Actor actor.Actor
	ID    string
}

type DeleteHandler struct {
	*support.Service
}

func (h DeleteHandler) Handle(ctx context.Context, cmd Delete) (cqrs.Nothing, error) {
	return cqrs.Nothing{}, h.Repo.Delete(ctx, domain.Delete{
		ID: cmd.ID, ActorID: cmd.Actor.ID, IP: cmd.Actor.IP, UserAgent: cmd.Actor.UserAgent, Now: h.Now(),
	})
}
