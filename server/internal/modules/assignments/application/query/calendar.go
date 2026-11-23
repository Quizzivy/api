package query

import (
	"context"

	"quizzivy/internal/modules/assignments/application/internal/support"
	"quizzivy/internal/modules/assignments/domain"
)

// CalendarWindows reads the published assignments Input's scope reaches whose
// own window opens or closes in Input's range, for the teacher's calendar
// (T-R8.5). Per-student overrides are not read.
type CalendarWindows struct {
	Input domain.CalendarInput
}

type CalendarWindowsHandler struct {
	*support.Service
}

func (s CalendarWindowsHandler) Handle(ctx context.Context, q CalendarWindows) ([]domain.CalendarWindow, error) {
	return s.Repo.CalendarWindows(ctx, q.Input)
}
