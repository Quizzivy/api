package command

import (
	"context"

	"quizzivy/internal/modules/timetable/application/internal/support"
	"quizzivy/internal/modules/timetable/domain"
	"quizzivy/internal/shared/actor"
	"quizzivy/internal/shared/content"
)

// UpdateSession applies Patch to session SessionID for Actor and returns the
// session as it now reads. On an occurrence without a row the row is written
// first, in the same transaction, keeping the date it was scheduled for; an
// occurrence back to its series, uncancelled and with no attendance, keeps no
// row; a patch that changes nothing writes nothing. A room or note over its
// limit, or a date over two years ahead, fails with a *validation.Error. One
// audit row, session.cancelled when the patch cancels it, else
// session.updated.
type UpdateSession struct {
	Actor     actor.Actor
	SessionID string
	Patch     domain.Patch
}

type UpdateSessionHandler struct {
	*support.Service
}

func (h UpdateSessionHandler) Handle(ctx context.Context, cmd UpdateSession) (domain.Session, error) {
	id, err := domain.ParseSessionID(cmd.SessionID)
	if err != nil {
		return domain.Session{}, err
	}
	patch := cmd.Patch
	var c content.Composer
	patch.Room.Value = domain.Text(&c, "room", patch.Room.Value, domain.MaxRoom)
	patch.Note.Value = domain.Text(&c, "note", patch.Note.Value, domain.MaxNote)
	if err := c.Err(); err != nil {
		return domain.Session{}, err
	}
	manager, err := h.Viewer(ctx, cmd.Actor.ID)
	if err != nil {
		return domain.Session{}, err
	}
	now := h.Now()
	found, err := h.Repo.EditSession(ctx, cmd.Actor.Scope, id, func(f domain.Found) (domain.Edit, error) {
		return patch.Apply(f, manager.Viewer, now)
	}, h.WriterOf(cmd.Actor))
	if err != nil {
		return domain.Session{}, err
	}
	return manager.Session(found), nil
}
