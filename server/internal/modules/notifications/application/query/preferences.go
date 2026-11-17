package query

import (
	"context"

	"quizzivy/internal/modules/notifications/application/internal/support"
	"quizzivy/internal/modules/notifications/domain"
)

// Preferences reads every switch for UserID in domain.Events' order: the
// saved value where there is one, Recipient's default where there is not, and
// for a student each email equal to its switch in the app (DG-82).
type Preferences struct {
	UserID    string
	Recipient domain.Recipient
}

type PreferencesHandler struct {
	*support.Service
}

func (s PreferencesHandler) Handle(ctx context.Context, q Preferences) ([]domain.Preference, error) {
	if q.UserID == "" {
		return nil, domain.ErrNoRecipient
	}
	stored, err := s.Repo.Preferences(ctx, q.UserID)
	if err != nil {
		return nil, err
	}
	return domain.WithDefaults(stored, q.Recipient), nil
}
