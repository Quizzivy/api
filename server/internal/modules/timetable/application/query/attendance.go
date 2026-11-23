package query

import (
	"context"

	"quizzivy/internal/modules/timetable/application/internal/support"
	"quizzivy/internal/modules/timetable/domain"
	"quizzivy/internal/shared/access"
)

// SessionAttendance asks for the register of session SessionID as Scope
// reaches it, for ViewerID: the class's active student members and anyone
// already marked on the session, each with their mark, note and rate over
// the term that holds today in ViewerID's zone, else over every session of
// the class. A session that has not started, or a cancelled one, is read all
// the same. A malformed id and one outside Scope answer domain.ErrNotFound.
type SessionAttendance struct {
	Scope     access.Scope
	ViewerID  string
	SessionID string
}

type SessionAttendanceHandler struct {
	*support.Service
}

func (h SessionAttendanceHandler) Handle(ctx context.Context, q SessionAttendance) (domain.Register, error) {
	id, err := domain.ParseSessionID(q.SessionID)
	if err != nil {
		return domain.Register{}, err
	}
	return h.Register(ctx, q.Scope, q.ViewerID, id)
}
