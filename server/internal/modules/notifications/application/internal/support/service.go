package support

import (
	"time"

	"quizzivy/internal/modules/notifications/application/ports"
	"quizzivy/internal/modules/notifications/domain"
)

// Service carries what the handlers share: the store, the clock, the
// throttle on materialising due items and the messaging context's count of
// a student's unread messages.
type Service struct {
	Repo     domain.Repository
	Now      func() time.Time
	Throttle *Throttle
	Messages ports.UnreadMessages
}

func NewService(repo domain.Repository) *Service {
	return &Service{Repo: repo, Now: time.Now, Throttle: NewThrottle(domain.DueEvery)}
}
