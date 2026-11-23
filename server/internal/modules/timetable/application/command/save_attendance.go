package command

import (
	"context"

	"quizzivy/internal/modules/timetable/application/internal/support"
	"quizzivy/internal/modules/timetable/domain"
	"quizzivy/internal/shared/actor"
)

// SaveAttendance sets, changes or removes the Marks it names on session
// SessionID for Actor, keeping every other student's, and returns the
// register as saved. A null status unmarks the student, removing their note;
// a note left out keeps the stored one, and a null or blank one clears it.
//
// Refused with *domain.RefusedMarks naming the students, before anything is
// read: a student named twice, a note beside a null status, a note over 500
// characters once composed; and, under the lock: a student neither an active
// member of the class nor marked on the session. A cancelled session is
// domain.ErrCancelled, one starting more than 15 minutes from now
// domain.ErrNotStarted, and one outside Actor's scope domain.ErrNotFound. An
// occurrence without a row gets one first, in the same transaction, once
// however many saves race for it. One attendance.saved audit row, or none
// when nothing changes.
type SaveAttendance struct {
	Actor     actor.Actor
	SessionID string
	Marks     []domain.MarkInput
}

type SaveAttendanceHandler struct {
	*support.Service
}

func (h SaveAttendanceHandler) Handle(ctx context.Context, cmd SaveAttendance) (domain.Register, error) {
	id, err := domain.ParseSessionID(cmd.SessionID)
	if err != nil {
		return domain.Register{}, err
	}
	marks, err := domain.CleanMarks(cmd.Marks)
	if err != nil {
		return domain.Register{}, err
	}
	by := h.WriterOf(cmd.Actor)
	if err := h.Repo.SaveMarks(ctx, cmd.Actor.Scope, id, func(f domain.Found) error {
		return f.TakesAttendance(by.Now)
	}, marks, by); err != nil {
		return domain.Register{}, err
	}
	return h.Register(ctx, cmd.Actor.Scope, cmd.Actor.ID, id)
}
