package query

import (
	"context"
	"time"

	"quizzivy/internal/modules/attempts/application/internal/support"
	"quizzivy/internal/modules/attempts/domain"
	"quizzivy/internal/shared/access"
)

// FlaggedAttempts reads StudentID's flagged attempts on the assignments Scope
// reaches, handed in or started at Since or later, oldest first: the
// context events of a staff member's direct thread with the student.
type FlaggedAttempts struct {
	Scope     access.Scope
	StudentID string
	Since     time.Time
}

type FlaggedAttemptsHandler struct {
	*support.Integrity
}

func (i FlaggedAttemptsHandler) Handle(ctx context.Context, q FlaggedAttempts) ([]domain.FlaggedAttempt, error) {
	return i.Repo.Flagged(ctx, q.Scope, q.StudentID, q.Since)
}
