package support

import (
	"log/slog"
	"time"

	"quizzivy/internal/modules/leads/domain"
)

// Service carries what the handlers share: the repository, the clock and the
// log a request that was not stored is noted in.
type Service struct {
	Repo   domain.Repository
	Now    func() time.Time
	Logger *slog.Logger
}
