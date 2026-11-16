package command

import (
	"context"

	"quizzivy/internal/modules/sharing/application/internal/support"
	"quizzivy/internal/modules/sharing/domain"
	"quizzivy/internal/shared/actor"
)

// UpdateShare sets the grant ShareID to Access (Can use or Can edit), for the
// resource's owner or scope.all. Its grantee, or anyone else who reads the
// resource through a grant, gets domain.ErrOwnerOnly; anyone else
// domain.ErrNotFound, as for a missing grant.
type UpdateShare struct {
	Actor   actor.Actor
	ShareID string
	Access  domain.Access
}

type UpdateShareHandler struct {
	*support.Service
}

func (s UpdateShareHandler) Handle(ctx context.Context, cmd UpdateShare) (domain.Grant, error) {
	share, err := s.Managed(ctx, cmd.Actor, cmd.ShareID)
	if err != nil {
		return domain.Grant{}, err
	}
	if _, err := domain.Transition(share.Access, cmd.Access); err != nil {
		return domain.Grant{}, err
	}
	grant, err := s.Repo.ChangeAccess(ctx, s.WriteBy(cmd.Actor), share, cmd.Access)
	if err != nil {
		return domain.Grant{}, err
	}
	s.SignPerson(ctx, &grant.Grantee, map[string]*string{})
	return grant, nil
}
