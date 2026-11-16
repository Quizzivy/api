package support

import (
	"time"

	"quizzivy/internal/modules/notifications/application/ports"
	"quizzivy/internal/modules/notifications/domain"
)

// Service carries what the handlers share: the store, the clock, the
// throttle on materialising due items, the messaging context's count of a
// student's unread messages, and the email channel, nil until it is wired.
type Service struct {
	Repo     domain.Repository
	Now      func() time.Time
	Throttle *Throttle
	Messages ports.UnreadMessages
	Mail     ports.Mailer
}

func NewService(repo domain.Repository) *Service {
	return &Service{Repo: repo, Now: time.Now, Throttle: NewThrottle(domain.DueEvery)}
}
