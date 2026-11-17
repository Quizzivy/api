package support

import (
	"time"

	"quizzivy/internal/modules/notifications/application/ports"
	"quizzivy/internal/modules/notifications/domain"
)

// Service carries what the handlers share: the store, the clock, the
// throttle on materialising due items, the messaging context's count of a
// student's unread messages, the email channel, nil until it is wired, and
// the scheduler's store with NOTIFY_EMAIL_EVENTS (Sends, nil for every
// email), nil until it is wired.
type Service struct {
	Repo     domain.Repository
	Now      func() time.Time
	Throttle *Throttle
	Messages ports.UnreadMessages
	Mail     ports.Mailer
	Schedule domain.Scheduled
	Sends    func(event string) bool
}

// EmailOn reports whether NOTIFY_EMAIL_EVENTS leaves template's email on.
func (s *Service) EmailOn(template domain.EmailTemplate) bool {
	return s.Sends == nil || s.Sends(template.Event())
}

func NewService(repo domain.Repository) *Service {
	return &Service{Repo: repo, Now: time.Now, Throttle: NewThrottle(domain.DueEvery)}
}
