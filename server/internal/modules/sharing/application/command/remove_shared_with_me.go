package command

import (
	"context"

	"quizzivy/internal/modules/sharing/application/internal/support"
	"quizzivy/internal/modules/sharing/domain"
	"quizzivy/internal/shared/actor"
	"quizzivy/internal/shared/cqrs"
)

// RemoveSharedWithMe is Remove from my list: the grant ShareID's grantee
// deletes it. Anyone else, the owner and scope.all included, gets
// domain.ErrNotFound: they stop sharing with DeleteShare.
type RemoveSharedWithMe struct {
	Actor   actor.Actor
	ShareID string
}

type RemoveSharedWithMeHandler struct {
	*support.Service
}

func (s RemoveSharedWithMeHandler) Handle(ctx context.Context, cmd RemoveSharedWithMe) (cqrs.Nothing, error) {
	share, err := s.Repo.Share(ctx, cmd.ShareID)
	if err != nil {
		return cqrs.Nothing{}, err
	}
	if share.GranteeID != cmd.Actor.ID {
		return cqrs.Nothing{}, domain.ErrNotFound
	}
	return cqrs.Nothing{}, s.Repo.Remove(ctx, s.WriteBy(cmd.Actor), share)
}
