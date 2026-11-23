package query

import (
	"context"

	"quizzivy/internal/modules/timetable/application/internal/support"
	"quizzivy/internal/modules/timetable/domain"
	"quizzivy/internal/shared/access"
)

// GetSession asks for session SessionID as Scope reaches it, a one-off's date
// read in ViewerID's zone. A malformed id, an occurrence its series does not
// have, and one outside Scope answer domain.ErrNotFound.
type GetSession struct {
	Scope     access.Scope
	ViewerID  string
	SessionID string
}

type GetSessionHandler struct {
	*support.Service
}

func (h GetSessionHandler) Handle(ctx context.Context, q GetSession) (domain.Session, error) {
	id, err := domain.ParseSessionID(q.SessionID)
	if err != nil {
		return domain.Session{}, err
	}
	found, err := h.Repo.Find(ctx, q.Scope, id)
	if err != nil {
		return domain.Session{}, err
	}
	manager, err := h.Viewer(ctx, q.ViewerID)
	if err != nil {
		return domain.Session{}, err
	}
	return manager.Session(found), nil
}
