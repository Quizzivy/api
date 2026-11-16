package command

import (
	"context"
	"unicode/utf8"

	notificationscommand "quizzivy/internal/modules/notifications/application/command"
	notificationsdomain "quizzivy/internal/modules/notifications/domain"
	"quizzivy/internal/modules/sharing/application/internal/support"
	"quizzivy/internal/modules/sharing/domain"
	"quizzivy/internal/shared/actor"
)

// CreateShares shares the test or question set Kind and ResourceID name with
// GranteeIDs at Access, for its owner or scope.all; a grantee gets
// domain.ErrOwnerOnly, since a recipient never shares further (D4), and anyone
// who cannot read it domain.ErrNotFound. GranteeIDs are normalised ids
// (uuid.UUID.String()); one named twice is domain.ErrInvalid, as the contract's
// uniqueItems compares the spellings only. With Notify each new recipient is
// told (content.shared), carrying Message when the sharer wrote one.
type CreateShares struct {
	Actor      actor.Actor
	Kind       domain.Kind
	ResourceID string
	GranteeIDs []string
	Access     domain.Access
	Notify     bool
	Message    *string
}

type CreateSharesHandler struct {
	*support.Service
}

func (s CreateSharesHandler) Handle(ctx context.Context, cmd CreateShares) (domain.Granted, error) {
	if !cmd.Access.Valid() || len(cmd.GranteeIDs) == 0 || len(cmd.GranteeIDs) > domain.MaxGrantees {
		return domain.Granted{}, domain.ErrInvalid
	}
	seen := make(map[string]bool, len(cmd.GranteeIDs))
	for _, id := range cmd.GranteeIDs {
		if seen[id] {
			return domain.Granted{}, domain.ErrInvalid
		}
		seen[id] = true
	}
	message := domain.CleanMessage(cmd.Message)
	if message != nil && utf8.RuneCountInString(*message) > domain.MaxMessage {
		return domain.Granted{}, domain.ErrInvalid
	}
	res, standing, err := s.Standing(ctx, cmd.Actor.Scope, cmd.Kind, cmd.ResourceID)
	if err != nil {
		return domain.Granted{}, err
	}
	if err := standing.Manage(); err != nil {
		return domain.Granted{}, err
	}
	granted, err := s.Repo.Grant(ctx, domain.GrantRequest{
		Write: s.WriteBy(cmd.Actor), Resource: res, GranteeIDs: cmd.GranteeIDs, Access: cmd.Access, Message: message,
	})
	if err != nil {
		return domain.Granted{}, err
	}
	signed := map[string]*string{}
	for i := range granted.Grants {
		s.SignPerson(ctx, &granted.Grants[i].Grantee, signed)
	}
	if cmd.Notify && len(granted.Grants) > 0 {
		s.announce(ctx, cmd.Actor.ID, res, granted.Grants, message)
	}
	return granted, nil
}

func (s CreateSharesHandler) announce(ctx context.Context, sharerID string, res domain.Resource, grants []domain.Grant, message *string) {
	s.Announce(ctx, "content shared", func(ctx context.Context) ([]notificationscommand.Notify, error) {
		sharer, err := s.Repo.Person(ctx, sharerID)
		if err != nil {
			return nil, err
		}
		out := make([]notificationscommand.Notify, len(grants))
		for i, g := range grants {
			out[i] = notificationscommand.Notify{
				UserID: g.Grantee.ID,
				Kind:   notificationsdomain.ContentShared,
				Params: notificationsdomain.Shared{
					SharerName: sharer.DisplayName, ContentKind: string(res.Kind), Name: res.Name, Access: string(g.Access), Message: message,
				},
				Target:    &notificationsdomain.Target{Route: notificationsdomain.RouteSharedWithMe},
				DedupeKey: notificationsdomain.SharedKey(string(res.Kind), res.ID),
				Merge:     notificationsdomain.Replace,
			}
		}
		return out, nil
	})
}
