package domain

import (
	"context"

	"quizzivy/internal/shared/paging"
)

// Repository stores the consultation requests.
type Repository interface {
	// Submit judges in.Submission in one transaction: it counts the requests
	// its number has stored from the last RepeatWindow before in.Now and asks
	// Verdict. A submission that is Stored is inserted, consented to at in.Now,
	// with one lead_received message enqueued for each address in the
	// settings' lead recipients; any other is left out. Every submission runs
	// the same count, so the two differ by the insert alone. It returns the
	// verdict.
	Submit(ctx context.Context, in Submit) (Drop, error)
	// List returns the page of q and how many requests match it.
	List(ctx context.Context, q ListQuery) ([]Lead, paging.Page, error)
	// Get returns the request id, or ErrNotFound.
	Get(ctx context.Context, id string) (Lead, error)
	// Update applies in.Change in one statement that also writes the
	// lead.updated audit row: a status that differs from the stored one records
	// in.ActorID and in.Now as who handled the request and when. The audit row
	// holds the status before and after and whether the note changed, never the
	// note, the name or the number. A change that leaves the row as it is
	// writes nothing. It returns the request as stored, or ErrNotFound.
	Update(ctx context.Context, in Update) (Lead, error)
	// Delete removes the request in one statement that also writes the
	// lead.deleted audit row, which names the request's id and nothing it
	// held. It returns ErrNotFound when no request has the id.
	Delete(ctx context.Context, in Delete) error
}
