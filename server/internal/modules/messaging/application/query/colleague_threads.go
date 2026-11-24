package query

import (
	"context"
	"strings"

	"quizzivy/internal/modules/messaging/application/internal/support"
	"quizzivy/internal/modules/messaging/domain"
)

// ColleagueThreads is the Colleagues tab of ActorID (F2, T-F2.4), searched
// by Query: the caller's threads with other staff members, and with a
// search the colleagues they could start one with. scope.all opens no one
// else's thread (D17).
type ColleagueThreads struct {
	ActorID string
	Query   string
}

type ColleagueThreadsHandler struct {
	*support.Service
}

func (s ColleagueThreadsHandler) Handle(ctx context.Context, q ColleagueThreads) ([]domain.ColleagueThread, error) {
	threads, err := s.Repo.ColleagueThreads(ctx, domain.ColleagueFilter{ActorID: q.ActorID, Query: strings.TrimSpace(q.Query)})
	if err != nil {
		return nil, err
	}
	signed := map[string]*string{}
	for i := range threads {
		threads[i].AvatarURL = s.Sign(ctx, threads[i].AvatarKey, signed)
	}
	return threads, nil
}
