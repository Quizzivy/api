package command

import (
	"context"

	"quizzivy/internal/modules/sharing/application/internal/support"
	"quizzivy/internal/shared/actor"
	"quizzivy/internal/shared/cqrs"
)

// DeleteShare is Stop sharing: it deletes the grant ShareID and nothing else,
// for the resource's owner or scope.all. Assignments the recipient made keep
// their version. Its grantee gets domain.ErrOwnerOnly (they leave with
// RemoveSharedWithMe); anyone else domain.ErrNotFound.
type DeleteShare struct {
	Actor   actor.Actor
	ShareID string
}

type DeleteShareHandler struct {
	*support.Service
}

func (s DeleteShareHandler) Handle(ctx context.Context, cmd DeleteShare) (cqrs.Nothing, error) {
	share, err := s.Managed(ctx, cmd.Actor, cmd.ShareID)
	if err != nil {
		return cqrs.Nothing{}, err
	}
	return cqrs.Nothing{}, s.Repo.Revoke(ctx, s.WriteBy(cmd.Actor), share)
}
