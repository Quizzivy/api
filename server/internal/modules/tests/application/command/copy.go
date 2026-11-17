package command

import (
	"context"

	"quizzivy/internal/modules/tests/application/internal/support"
	"quizzivy/internal/modules/tests/domain"
)

// Copy is "Make a copy" (copyTest, D4, T-R7.7): the current draft of any test
// the actor can read, a share included, becomes a new draft the actor owns,
// with its sections, instructions and its own groups, and a new bank question
// of the actor's for each of its standalone questions. Versions, assignments
// and grants stay behind. It is named as Duplicate names its copies;
// Duplicate, the owner's, keeps referencing the bank questions.
type Copy struct {
	Request domain.Request
}

type CopyHandler struct {
	*support.Service
}

func (s CopyHandler) Handle(ctx context.Context, cmd Copy) (domain.Test, error) {
	return s.Repo.Copy(ctx, domain.DuplicateInput{
		ID:        cmd.Request.ID,
		ActorID:   cmd.Request.ActorID,
		Now:       s.Now(),
		IP:        cmd.Request.IP,
		UserAgent: cmd.Request.UserAgent,
		Scope:     cmd.Request.Scope,
	})
}
