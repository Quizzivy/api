package command

import (
	"context"
	"time"
	"unicode/utf8"

	"quizzivy/internal/modules/notifications/application/internal/support"
	"quizzivy/internal/modules/notifications/domain"
	"quizzivy/internal/shared/cqrs"
)

// MarkRead marks those of IDs that are UserID's own, unread and of Audience's
// kinds (every kind when Audience is empty). An id that is another user's,
// another console's, already read or unknown is skipped without a word, so the
// outcome says nothing about an id that is not the user's. No id at all, and
// more than domain.MaxMarkedIDs of them, and an Audience that is neither the
// teacher's nor the student's, are refused.
type MarkRead struct {
	UserID   string
	IDs      []string
	Audience domain.Audience
}

type MarkReadHandler struct {
	*support.Service
}

func (s MarkReadHandler) Handle(ctx context.Context, cmd MarkRead) (cqrs.Nothing, error) {
	switch {
	case cmd.UserID == "":
		return cqrs.Nothing{}, domain.ErrNoRecipient
	case len(cmd.IDs) == 0:
		return cqrs.Nothing{}, domain.ErrNoIDs
	case len(cmd.IDs) > domain.MaxMarkedIDs:
		return cqrs.Nothing{}, domain.ErrTooManyIDs
	case !cmd.Audience.Valid():
		return cqrs.Nothing{}, domain.ErrUnknownAudience
	}
	return cqrs.Nothing{}, s.Repo.MarkRead(ctx, cmd.UserID, cmd.IDs, cmd.Audience)
}

// MarkAllRead marks every unread notification UserID has of Audience's kinds,
// or of every kind when Audience is empty.
type MarkAllRead struct {
	UserID   string
	Audience domain.Audience
}

type MarkAllReadHandler struct {
	*support.Service
}

func (s MarkAllReadHandler) Handle(ctx context.Context, cmd MarkAllRead) (cqrs.Nothing, error) {
	switch {
	case cmd.UserID == "":
		return cqrs.Nothing{}, domain.ErrNoRecipient
	case !cmd.Audience.Valid():
		return cqrs.Nothing{}, domain.ErrUnknownAudience
	}
	return cqrs.Nothing{}, s.Repo.MarkAllRead(ctx, cmd.UserID, cmd.Audience)
}

// MarkKeyRead marks read the notification UserID holds under DedupeKey,
// when it is unread and was last written no later than WrittenBy: what a
// producer does once the reader has seen what the item tells, without
// clearing a notice written after it looked. No user and a key out of bounds
// are refused; a key the user does not hold changes nothing.
type MarkKeyRead struct {
	UserID    string
	DedupeKey string
	WrittenBy time.Time
}

type MarkKeyReadHandler struct {
	*support.Service
}

func (s MarkKeyReadHandler) Handle(ctx context.Context, cmd MarkKeyRead) (cqrs.Nothing, error) {
	switch length := utf8.RuneCountInString(cmd.DedupeKey); {
	case cmd.UserID == "":
		return cqrs.Nothing{}, domain.ErrNoRecipient
	case length < 1 || length > domain.MaxDedupeKey:
		return cqrs.Nothing{}, domain.ErrInvalidDedupeKey
	}
	return cqrs.Nothing{}, s.Repo.MarkKeyRead(ctx, cmd.UserID, cmd.DedupeKey, cmd.WrittenBy)
}
