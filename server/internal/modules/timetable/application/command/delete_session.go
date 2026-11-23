package command

import (
	"context"

	"quizzivy/internal/modules/timetable/application/internal/support"
	"quizzivy/internal/modules/timetable/domain"
	"quizzivy/internal/shared/actor"
	"quizzivy/internal/shared/cqrs"
)

// DeleteSession removes the one-off SessionID for Actor. An occurrence of a
// series fails with domain.ErrInSeries (it is cancelled instead), and a
// session with attendance with domain.ErrHasAttendance; nothing is removed.
// One session.deleted audit row.
type DeleteSession struct {
	Actor     actor.Actor
	SessionID string
}

type DeleteSessionHandler struct {
	*support.Service
}

func (h DeleteSessionHandler) Handle(ctx context.Context, cmd DeleteSession) (cqrs.Nothing, error) {
	id, err := domain.ParseSessionID(cmd.SessionID)
	if err != nil {
		return cqrs.Nothing{}, err
	}
	return cqrs.Nothing{}, h.Repo.DeleteOneOff(ctx, cmd.Actor.Scope, id, h.WriterOf(cmd.Actor))
}
