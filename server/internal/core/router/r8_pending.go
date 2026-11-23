package router

import (
	"context"

	"quizzivy/gen/openapi"
	"quizzivy/internal/platform/httpx"
)

// R8Pending answers 501 for the R8 schedule operations the contract declares
// and no task has built yet (httpx.ErrNotImplemented): the calendar (T-R8.5)
// and the student's week (T-R8.8). The terms (T-R8.3) are the organization
// module's, and the sessions and weekly series (T-R8.4) and attendance
// (T-R8.6) the timetable module's. Each remaining task moves its operations
// into its module's transport and deletes them here; the last one deletes the
// file.
type R8Pending struct{}

// T-R8.5

func (R8Pending) GetCalendar(context.Context, openapi.GetCalendarRequestObject) (openapi.GetCalendarResponseObject, error) {
	return nil, httpx.ErrNotImplemented
}

// T-R8.8

func (R8Pending) GetStudentWeek(context.Context, openapi.GetStudentWeekRequestObject) (openapi.GetStudentWeekResponseObject, error) {
	return nil, httpx.ErrNotImplemented
}
