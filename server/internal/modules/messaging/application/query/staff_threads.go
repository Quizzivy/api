// Package query is the messaging context's reads on the staff side: the
// Messages list and one thread's page.
package query

import (
	"context"
	"strings"

	"quizzivy/internal/modules/messaging/application/internal/support"
	"quizzivy/internal/modules/messaging/domain"
)

// StaffThreads is the staff Messages list of ActorID on Tab (all when
// unset), searched by Query. All widens the class channels, never the direct
// threads (D17, DG-08).
type StaffThreads struct {
	ActorID string
	All     bool
	Tab     domain.Tab
	Query   string
}

type StaffThreadsHandler struct {
	*support.Service
}

func (s StaffThreadsHandler) Handle(ctx context.Context, q StaffThreads) ([]domain.StaffThread, error) {
	tab := q.Tab
	if tab == "" {
		tab = domain.TabAll
	}
	if !tab.Valid() {
		return nil, domain.ErrInvalid
	}
	threads, err := s.Repo.Threads(ctx, domain.ThreadFilter{ActorID: q.ActorID, All: q.All, Tab: tab, Query: strings.TrimSpace(q.Query)})
	if err != nil {
		return nil, err
	}
	signed := map[string]*string{}
	for i := range threads {
		threads[i].AvatarURL = s.Sign(ctx, threads[i].AvatarKey, signed)
	}
	return threads, nil
}
