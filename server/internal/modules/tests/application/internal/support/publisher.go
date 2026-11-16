package support

import (
	"log/slog"
	"quizzivy/internal/modules/tests/application/ports"
	"quizzivy/internal/modules/tests/domain"
	"time"
)

// Publisher carries what the publisher handlers share: their ports and the
// helpers they call. Notifier, nil until the application is given one, tells
// an owner of a co-editor's publish.
type Publisher struct {
	Repo     domain.Repository
	Now      func() time.Time
	Notifier ports.Notifier
	Logger   *slog.Logger
}

func NewPublisher(repo domain.Repository) *Publisher {
	return &Publisher{Repo: repo, Now: time.Now}
}
