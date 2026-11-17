package command

import (
	"context"
	"quizzivy/internal/modules/questions/application/internal/support"
	"quizzivy/internal/modules/questions/domain"
	"quizzivy/internal/shared/cqrs"
)

type Delete struct {
	Request domain.WriteRequest
}

type DeleteHandler struct {
	*support.Service
}

// Handle deletes the question for its owner and scope.all; a caller who reads
// it through a share gets ErrOwnerOnly.
func (s DeleteHandler) Handle(ctx context.Context, cmd Delete) (cqrs.Nothing, error) {
	if !cmd.Request.All {
		level, err := s.Repo.Access(ctx, cmd.Request.Scope(), cmd.Request.ID)
		if err == nil && !level.Owner() {
			return cqrs.Nothing{}, domain.ErrOwnerOnly
		}
	}
	return cqrs.Nothing{}, s.Repo.SoftDelete(ctx, domain.WriteInput{
		ID:        cmd.Request.ID,
		ActorID:   cmd.Request.ActorID,
		All:       cmd.Request.All,
		Now:       s.Now(),
		IP:        cmd.Request.IP,
		UserAgent: cmd.Request.UserAgent,
	})
}
