package command

import (
	"context"

	"quizzivy/internal/modules/messaging/application/internal/support"
	"quizzivy/internal/modules/messaging/domain"
	"quizzivy/internal/shared/actor"
)

// PostColleague is a staff member's message to ColleagueID, another staff
// member (F2, T-F2.4): text, a file or both, either side may attach. The
// first post makes the thread, one per pair; a repeated ClientID returns the
// message the first call made. An account that is no colleague of the
// caller answers domain.ErrNotFound, and a colleague who is disabled or no
// longer opens the teacher workspace domain.ErrNotAllowed. The reader is
// told through the bell, as in a student's thread. Not audited.
type PostColleague struct {
	Actor        actor.Actor
	ColleagueID  string
	ClientID     string
	Body         *string
	AttachmentID *string
}

type PostColleagueHandler struct {
	*support.Service
}

func (s PostColleagueHandler) Handle(ctx context.Context, cmd PostColleague) (domain.Posted, error) {
	return post(ctx, s.Service, cmd.Actor.ID, cmd.ClientID, cmd.Body, cmd.AttachmentID, func(p domain.Post) (domain.Posted, error) {
		return s.Repo.PostColleague(ctx, cmd.Actor.ID, cmd.ColleagueID, p)
	})
}
