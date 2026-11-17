package command

import (
	"context"
	"quizzivy/internal/modules/questions/domain"
)

// TransferOwnership gives every question and question set From owns to To.
type TransferOwnership struct {
	From string
	To   string
}

// TransferOwnershipHandler moves questions and question sets through Repo,
// which is bound to the transaction the whole transfer runs in.
type TransferOwnershipHandler struct {
	Repo domain.OwnershipTransfer
}

// Handle returns what moved, as domain.OwnershipTransfer counts it.
func (h TransferOwnershipHandler) Handle(ctx context.Context, cmd TransferOwnership) (domain.Transferred, error) {
	return h.Repo.TransferOwnership(ctx, cmd.From, cmd.To)
}
