package command

import (
	"context"

	"quizzivy/internal/modules/organization/application/internal/support"
	"quizzivy/internal/modules/organization/domain"
	"quizzivy/internal/shared/actor"
)

// CreateTerm adds Draft to the centre's terms for Actor and returns it with
// its state on Actor's today. The draft is normalised first, so a blank name
// or an end before the start fails with a *validation.Error; a range that
// shares a day with another term fails with *domain.TermOverlap, as the
// database judges it inside the write. One term.created audit row.
type CreateTerm struct {
	Actor actor.Actor
	Draft domain.TermDraft
}

type CreateTermHandler struct {
	*support.Service
}

func (h CreateTermHandler) Handle(ctx context.Context, cmd CreateTerm) (domain.StatedTerm, error) {
	terms, err := h.TermStore()
	if err != nil {
		return domain.StatedTerm{}, err
	}
	draft, err := cmd.Draft.Normalised()
	if err != nil {
		return domain.StatedTerm{}, err
	}
	return stated(ctx, h.Service, cmd.Actor)(terms.Create(ctx, draft, writerOf(cmd.Actor, h.Service)))
}

// stated returns a function that gives a written term its state on a's today.
func stated(ctx context.Context, s *support.Service, a actor.Actor) func(domain.Term, error) (domain.StatedTerm, error) {
	return func(t domain.Term, err error) (domain.StatedTerm, error) {
		if err != nil {
			return domain.StatedTerm{}, err
		}
		today, err := s.TodayOf(ctx, a.ID)
		if err != nil {
			return domain.StatedTerm{}, err
		}
		return domain.StatedTerm{Term: t, State: t.StateOn(today)}, nil
	}
}

func writerOf(a actor.Actor, s *support.Service) domain.TermWriter {
	return domain.TermWriter{ActorID: a.ID, IP: a.IP, UserAgent: a.UserAgent, Now: s.Now()}
}
