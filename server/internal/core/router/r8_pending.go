package router

import (
	"context"

	"quizzivy/gen/openapi"
	"quizzivy/internal/platform/httpx"
)

// R8Pending answers 501 for the R8 schedule operations the contract declares
// and no task has built yet (httpx.ErrNotImplemented): attendance (T-R8.6)
// and the student's week (T-R8.8). The terms (T-R8.3) are the organization
// module's, and the sessions, weekly series (T-R8.4) and the calendar (T-R8.5)
// the timetable module's. Each remaining task moves its operations into its
// module's transport and deletes them here; the last one deletes the file.
type R8Pending struct{}

// T-R8.6

func (R8Pending) ListAttendanceSessions(context.Context, openapi.ListAttendanceSessionsRequestObject) (openapi.ListAttendanceSessionsResponseObject, error) {
	return nil, httpx.ErrNotImplemented
}

func (R8Pending) GetSessionAttendance(context.Context, openapi.GetSessionAttendanceRequestObject) (openapi.GetSessionAttendanceResponseObject, error) {
	return nil, httpx.ErrNotImplemented
}

func (R8Pending) SaveSessionAttendance(context.Context, openapi.SaveSessionAttendanceRequestObject) (openapi.SaveSessionAttendanceResponseObject, error) {
	return nil, httpx.ErrNotImplemented
}

// T-R8.8

func (R8Pending) GetStudentWeek(context.Context, openapi.GetStudentWeekRequestObject) (openapi.GetStudentWeekResponseObject, error) {
	return nil, httpx.ErrNotImplemented
}
