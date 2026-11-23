package query

import (
	"context"
	"quizzivy/internal/modules/classes/application/internal/support"
	"quizzivy/internal/modules/classes/domain"
	"quizzivy/internal/shared/access"
)

// Get reads one class Scope reaches, with its average score and the timetable
// of its active series; another teacher's answers ErrNotFound.
type Get struct {
	ClassID string
	Scope   access.Scope
}

type GetHandler struct {
	*support.Service
}

func (s GetHandler) Handle(ctx context.Context, q Get) (domain.Class, error) {
	class, err := s.Repo.Get(ctx, q.Scope, q.ClassID)
	if err != nil {
		return domain.Class{}, err
	}
	if err := s.AttachSchedule(ctx, &class); err != nil {
		return domain.Class{}, err
	}
	return class, s.AttachAverage(ctx, &class)
}
