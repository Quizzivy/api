package domain

import (
	"context"

	"quizzivy/internal/shared/paging"
)

// Repository persists grants in app.content_shares and reads the people they
// name. Every write audits itself in the statement that makes it.
type Repository interface {
	// Directory returns at most limit accounts that open the teacher
	// workspace, active or disabled, never actorID, matching query on the
	// names and the email accent-insensitively, best match first; by name
	// without a query.
	Directory(ctx context.Context, actorID, query string, limit int) ([]Person, error)
	// Person returns the user id, or ErrNotFound.
	Person(ctx context.Context, id string) (Person, error)
	// Holds reports whether userID holds a grant to the live resource.
	Holds(ctx context.Context, kind Kind, resourceID, userID string) (bool, error)
	// Grants returns the grants to the resource, oldest first.
	Grants(ctx context.Context, kind Kind, resourceID string) ([]Grant, error)
	// Grant shares the live resource with each recipient Skip lets through,
	// in the order named, and audits each grant; ErrNotFound when the resource
	// is gone.
	Grant(ctx context.Context, req GrantRequest) (Granted, error)
	// Share returns the grant id as its row stores it, or ErrNotFound.
	Share(ctx context.Context, id string) (Share, error)
	// ChangeAccess sets the grant to access, auditing a change; ErrNotFound
	// when the grant is gone.
	ChangeAccess(ctx context.Context, w Write, share Share, access Access) (Grant, error)
	// Revoke deletes the grant for the resource's owner (Stop sharing), and
	// Remove for its grantee (Remove from my list), each auditing it;
	// ErrNotFound when the grant is gone.
	Revoke(ctx context.Context, w Write, share Share) error
	Remove(ctx context.Context, w Write, share Share) error
	// SharedWithMe returns one page of the live grants to f.UserID, newest
	// first, with the counts per kind under f.Query.
	SharedWithMe(ctx context.Context, f SharedFilter) ([]SharedItem, Counts, paging.Page, error)
	// CountSharedWithMe is the number of live grants to userID.
	CountSharedWithMe(ctx context.Context, userID string) (int, error)
}
