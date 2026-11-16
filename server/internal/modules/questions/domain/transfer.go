package domain

import "context"

// Transferred counts what an ownership transfer moved: the questions and the
// question sets not deleted.
type Transferred struct {
	Questions int
	Sets      int
}

// OwnershipTransfer gives every question and question set one account owns to
// another.
type OwnershipTransfer interface {
	// TransferOwnership moves the questions of from to to and then its
	// question sets, deleted ones too, locking each table's rows in id order,
	// and counts those not deleted. A set's items name questions of from,
	// which move in the same call, so a set still holds only its owner's
	// questions. The grants to the sets follow them: a grant stores no owner.
	TransferOwnership(ctx context.Context, from, to string) (Transferred, error)
}
