package command

import (
	"context"

	"quizzivy/internal/modules/leads/application/internal/support"
	"quizzivy/internal/modules/leads/domain"
	"quizzivy/internal/shared/actor"
)

// Update records how Actor handled the request ID: Change's status, its note,
// or both. A note that cannot be stored fails with a *validation.Error naming
// note, and a missing request with domain.ErrNotFound. A status that differs
// from the stored one names Actor and the time as who handled the request and
// when. It returns the request as stored.
type Update struct {
	Actor  actor.Actor
	ID     string
	Change domain.Change
}

type UpdateHandler struct {
	*support.Service
}

func (h UpdateHandler) Handle(ctx context.Context, cmd Update) (domain.Lead, error) {
	change, err := cmd.Change.Normalised()
	if err != nil {
		return domain.Lead{}, err
	}
	return h.Repo.Update(ctx, domain.Update{
		ID: cmd.ID, Change: change, ActorID: cmd.Actor.ID, IP: cmd.Actor.IP, UserAgent: cmd.Actor.UserAgent, Now: h.Now(),
	})
}
