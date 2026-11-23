package command

import (
	"context"

	"quizzivy/internal/modules/organization/application/internal/support"
	"quizzivy/internal/modules/organization/domain"
	"quizzivy/internal/shared/actor"
)

// UpdateTerm changes term ID by Patch for Actor and returns it with its state
// on Actor's today. The range is judged as it
// stands after the change, as CreateTerm judges a new one; an unknown id
// fails with domain.ErrTermNotFound. A patch that changes nothing writes
// nothing; any other writes one term.updated audit row naming the fields that
// changed.
type UpdateTerm struct {
	Actor actor.Actor
	ID    string
	Patch domain.TermPatch
}

type UpdateTermHandler struct {
	*support.Service
}

func (h UpdateTermHandler) Handle(ctx context.Context, cmd UpdateTerm) (domain.StatedTerm, error) {
	terms, err := h.TermStore()
	if err != nil {
		return domain.StatedTerm{}, err
	}
	return stated(ctx, h.Service, cmd.Actor)(terms.Update(ctx, cmd.ID, func(stored domain.Term) (domain.TermDraft, error) {
		return cmd.Patch.Over(stored).Normalised()
	}, writerOf(cmd.Actor, h.Service)))
}
