package support

import (
	"context"
	"errors"
	"log/slog"
	"time"

	notificationscommand "quizzivy/internal/modules/notifications/application/command"
	"quizzivy/internal/modules/sharing/application/ports"
	"quizzivy/internal/modules/sharing/domain"
	"quizzivy/internal/shared/access"
	"quizzivy/internal/shared/actor"
	"quizzivy/internal/shared/cqrs"
)

// Service carries what the handlers share: the grants, the content they name,
// the photo signer, the notifier and the clock.
type Service struct {
	Repo     domain.Repository
	Content  ports.Content
	Avatars  ports.Avatars
	Notifier ports.Notifier
	Logger   *slog.Logger
	Now      func() time.Time
}

// Standing resolves the resource kind and id names and what scope is to it:
// its owner (or scope.all), a grantee, who reads it through a grant even
// where its own module does not widen its reads yet, or a stranger.
func (s *Service) Standing(ctx context.Context, scope access.Scope, kind domain.Kind, id string) (domain.Resource, domain.Standing, error) {
	if !kind.Valid() {
		return domain.Resource{}, domain.Stranger, domain.ErrInvalid
	}
	res, err := s.Content.Resource(ctx, scope, kind, id)
	readable := err == nil
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return domain.Resource{}, domain.Stranger, err
	}
	holds := false
	if !readable && scope.UserID != "" {
		if holds, err = s.Repo.Holds(ctx, kind, id, scope.UserID); err != nil {
			return domain.Resource{}, domain.Stranger, err
		}
	}
	return res, domain.StandingOf(scope, res, readable, holds), nil
}

// SignPerson fills p's AvatarURL from its key, once per key in signed.
func (s *Service) SignPerson(ctx context.Context, p *domain.Person, signed map[string]*string) {
	p.AvatarURL = s.sign(ctx, p.AvatarKey, signed)
}

// SignOwner fills o's AvatarURL from its key, once per key in signed.
func (s *Service) SignOwner(ctx context.Context, o *domain.SharedOwner, signed map[string]*string) {
	o.AvatarURL = s.sign(ctx, o.AvatarKey, signed)
}

func (s *Service) sign(ctx context.Context, key *string, signed map[string]*string) *string {
	if key == nil || *key == "" || s.Avatars == nil {
		return nil
	}
	if url, seen := signed[*key]; seen {
		return url
	}
	var out *string
	if url, err := s.Avatars.AvatarURL(ctx, *key); err == nil && url != "" {
		out = &url
	}
	signed[*key] = out
	return out
}

// WriteBy is the write who makes now.
func (s *Service) WriteBy(who actor.Actor) domain.Write {
	return domain.Write{ActorID: who.ID, All: who.Scope.All, Now: s.Now(), IP: who.IP, UserAgent: who.UserAgent}
}

// Managed returns the grant id when who may change it: the owner of its
// resource or scope.all. Another reader of the resource gets ErrOwnerOnly, and
// anyone else ErrNotFound, as for a missing grant.
func (s *Service) Managed(ctx context.Context, who actor.Actor, id string) (domain.Share, error) {
	share, err := s.Repo.Share(ctx, id)
	if err != nil {
		return domain.Share{}, err
	}
	_, standing, err := s.Standing(ctx, who.Scope, share.Kind, share.ResourceID)
	if err != nil {
		return domain.Share{}, err
	}
	if err := standing.Manage(); err != nil {
		return domain.Share{}, err
	}
	return share, nil
}

// Announce tells whom build names through the notifier, after the change it
// follows, never failing it.
func (s *Service) Announce(ctx context.Context, what string, build func(context.Context) ([]notificationscommand.Notify, error)) {
	cqrs.Announce(ctx, s.Notifier, s.Logger, what, build)
}
