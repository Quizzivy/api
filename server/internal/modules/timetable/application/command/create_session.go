// Package command is the timetable's writes. Each checks R2's class scope and
// runs under the class row's lock in its repository, so another teacher's
// class, session or series answers domain.ErrNotFound as a missing one does.
package command

import (
	"context"
	"time"

	"quizzivy/internal/modules/timetable/application/internal/support"
	"quizzivy/internal/modules/timetable/domain"
	"quizzivy/internal/shared/actor"
	"quizzivy/internal/shared/content"
)

// CreateSession adds Draft to class ClassID for Actor and returns the one-off,
// or the new series' first occurrence, on the draft's date. The date and time
// are read in Actor's zone, which a series keeps; a repeat runs to the draft's
// end, else to the last day of the term that holds its date, else until it is
// stopped; the room defaults to the class's own. A draft out of bounds fails
// with a *validation.Error naming the field, an archived class with
// domain.ErrClassArchived once it is found. One session.created audit row.
type CreateSession struct {
	Actor   actor.Actor
	ClassID string
	Draft   domain.Draft
}

type CreateSessionHandler struct {
	*support.Service
}

func (h CreateSessionHandler) Handle(ctx context.Context, cmd CreateSession) (domain.Session, error) {
	zone, err := h.ZoneOf(ctx, cmd.Actor.ID)
	if err != nil {
		return domain.Session{}, err
	}
	loc := domain.LocationOf(zone)
	draft := cmd.Draft
	var c content.Composer
	draft.Room.Value = domain.Text(&c, "room", draft.Room.Value, domain.MaxRoom)
	if err := c.Err(); err != nil {
		return domain.Session{}, err
	}
	if err := draft.Validate(domain.Today(h.Now(), loc)); err != nil {
		return domain.Session{}, err
	}
	var termEnd *time.Time
	if draft.RepeatWeekly && draft.EndsOn == nil {
		if termEnd, err = h.TermEnd(ctx, draft.Date); err != nil {
			return domain.Session{}, err
		}
	}
	created, err := h.Repo.Create(ctx, cmd.Actor.Scope, cmd.ClassID, func(classRoom *string) (*domain.Series, *domain.NewOneOff, error) {
		room := classRoom
		if draft.Room.Set {
			room = draft.Room.Value
		}
		if draft.RepeatWeekly {
			s := draft.Series(cmd.ClassID, zone, termEnd, room)
			return &s, nil, nil
		}
		return nil, &domain.NewOneOff{StartsAt: draft.Start.On(draft.Date, loc), DurationMinutes: draft.DurationMinutes, Room: room}, nil
	}, h.WriterOf(cmd.Actor))
	if err != nil {
		return domain.Session{}, err
	}
	manager := domain.OccurrenceManager{Viewer: loc}
	if created.Series != nil {
		return manager.Occurrence(*created.Series, nil, draft.Date), nil
	}
	return manager.Session(domain.Found{ID: domain.OneOffID(created.Row.ID), Row: created.Row}), nil
}
