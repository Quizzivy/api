package command

import (
	"context"

	"quizzivy/internal/modules/sharing/domain"
)

// TransferOwnership follows an account's content transfer to To, made by
// Write's actor: the grants to the moved tests and sets stay as they are, and
// a grant to To, who now owns what it names, is deleted.
type TransferOwnership struct {
	domain.Write
	To string
}

// TransferOwnershipHandler deletes those grants through Repo, which is bound
// to the transaction the whole transfer runs in.
type TransferOwnershipHandler struct {
	Repo domain.OwnershipTransfer
}

// Handle returns how many grants it deleted.
func (h TransferOwnershipHandler) Handle(ctx context.Context, cmd TransferOwnership) (int, error) {
	return h.Repo.DropOwnGrants(ctx, cmd.Write, cmd.To)
}
