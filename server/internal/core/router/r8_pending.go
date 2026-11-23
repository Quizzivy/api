package router

import (
	"context"

	"quizzivy/gen/openapi"
	"quizzivy/internal/platform/httpx"
)

// R8Pending answers 501 for the R8 schedule operations the contract declares
// and no task has built yet (httpx.ErrNotImplemented): the student's week
// (T-R8.8). The terms (T-R8.3) are the organization module's, and the
// sessions and weekly series (T-R8.4), the calendar (T-R8.5) and attendance
// (T-R8.6) the timetable module's. The task that builds the student's week
// moves it into its module's transport and deletes this file.
type R8Pending struct{}

// T-R8.8

func (R8Pending) GetStudentWeek(context.Context, openapi.GetStudentWeekRequestObject) (openapi.GetStudentWeekResponseObject, error) {
	return nil, httpx.ErrNotImplemented
}
