package query

import (
	"context"
	"quizzivy/internal/modules/identity/application/internal/support"
	"quizzivy/internal/modules/identity/domain"
	"quizzivy/internal/shared/access"
)

// ListSessions backs GET /auth/sessions: the caller's live sessions at the
// host the request came from. RefreshToken is the refresh cookie of the
// request, which makes the caller's own session current.
type ListSessions struct {
	UserID       string
	Audience     access.Audience
	RefreshToken string
}

type ListSessionsHandler struct {
	*support.Service
}

func (s ListSessionsHandler) Handle(ctx context.Context, q ListSessions) ([]domain.Session, error) {
	return s.Users.ListSessions(ctx, domain.SessionsQuery{
		UserID:           q.UserID,
		Audience:         q.Audience,
		CurrentTokenHash: support.TokenHash(q.RefreshToken),
		Now:              s.Now(),
		Limit:            domain.MaxSessions,
	})
}
