package command

import (
	"context"

	"quizzivy/internal/modules/organization/application/internal/support"
	"quizzivy/internal/shared/actor"
	"quizzivy/internal/shared/cqrs"
)

// DeleteTerm removes term ID for Actor, or fails with domain.ErrTermNotFound.
// Nothing else changes: attendance rates read the terms when they are
// computed, and a series that ran to the term's end keeps its last date. One
// term.deleted audit row.
type DeleteTerm struct {
	Actor actor.Actor
	ID    string
}

type DeleteTermHandler struct {
	*support.Service
}

func (h DeleteTermHandler) Handle(ctx context.Context, cmd DeleteTerm) (cqrs.Nothing, error) {
	terms, err := h.TermStore()
	if err != nil {
		return cqrs.Nothing{}, err
	}
	return cqrs.Nothing{}, terms.Delete(ctx, cmd.ID, writerOf(cmd.Actor, h.Service))
}
