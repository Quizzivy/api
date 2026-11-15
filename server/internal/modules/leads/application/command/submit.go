package command

import (
	"context"

	"quizzivy/internal/modules/leads/application/internal/support"
	"quizzivy/internal/modules/leads/domain"
	"quizzivy/internal/shared/cqrs"
)

// Submit takes what the consultation form sent. Form is normalised first, and
// a name or a number that cannot be stored fails with a *validation.Error
// naming the field. Every other form succeeds, whether it was stored or not:
// one that filled the unseen field, and one whose number already has
// domain.RepeatCap requests stored from the last domain.RepeatWindow, is left
// out and noted in the log as lead_dropped with its reason and nothing the
// person typed. The result says nothing, so a caller cannot tell the two
// apart.
type Submit struct {
	Form domain.Form
}

type SubmitHandler struct {
	*support.Service
}

func (h SubmitHandler) Handle(ctx context.Context, cmd Submit) (cqrs.Nothing, error) {
	submission, err := cmd.Form.Normalised()
	if err != nil {
		return cqrs.Nothing{}, err
	}
	verdict, err := h.Repo.Submit(ctx, domain.Submit{Submission: submission, Now: h.Now()})
	if err != nil {
		return cqrs.Nothing{}, err
	}
	if verdict != domain.Stored {
		h.Logger.InfoContext(ctx, "lead_dropped", "reason", string(verdict))
	}
	return cqrs.Nothing{}, nil
}
