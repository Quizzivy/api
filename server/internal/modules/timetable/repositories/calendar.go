package repositories

import (
	"context"
	"fmt"

	"quizzivy/internal/modules/timetable/domain"
	"quizzivy/internal/platform/db"
	"quizzivy/internal/shared/access"
	"quizzivy/internal/shared/opt"
)

// Reaches answers domain.ErrNotFound unless scope reaches the class, reading
// without a lock.
func (p *Postgres) Reaches(ctx context.Context, scope access.Scope, classID string) error {
	found, err := db.Exists(ctx, p, `SELECT 1 FROM app.classes c WHERE c.id = $3::uuid AND `+reached,
		scope.All, opt.String(scope.UserID), classID)
	if err != nil {
		return fmt.Errorf("timetable: read class %s: %w", classID, err)
	}
	if !found {
		return domain.ErrNotFound
	}
	return nil
}
