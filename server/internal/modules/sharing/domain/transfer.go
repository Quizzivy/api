package domain

import "context"

// OwnershipTransfer is the sharing side of an account's content transfer. The
// tests and question sets have moved already, and their grants with them,
// since a grant stores no owner; what is left is a grant whose grantee is now
// the owner.
type OwnershipTransfer interface {
	// DropOwnGrants deletes every grant whose grantee owns its resource, the
	// tests and sets ownerID was just given among them, deleted ones too, and
	// audits each as share.revoked by w's actor, in one statement. It returns
	// how many it deleted.
	DropOwnGrants(ctx context.Context, w Write, ownerID string) (int, error)
}
