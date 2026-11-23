package router

import (
	"context"

	"quizzivy/gen/openapi"
	"quizzivy/internal/platform/httpx"
)

// R8Pending answers 501 for the R8 schedule operations the contract declares
// and no task has built yet (httpx.ErrNotImplemented): sessions and weekly
// series (T-R8.4), the calendar (T-R8.5), attendance (T-R8.6) and the
// student's week (T-R8.8). The terms (T-R8.3) are the organization module's. Each of those tasks moves its
// operations into its module's transport and deletes them here; the last one
// deletes the file.
type R8Pending struct{}

// T-R8.4

func (R8Pending) CreateClassSession(context.Context, openapi.CreateClassSessionRequestObject) (openapi.CreateClassSessionResponseObject, error) {
	return nil, httpx.ErrNotImplemented
}

func (R8Pending) UpdateClassSession(context.Context, openapi.UpdateClassSessionRequestObject) (openapi.UpdateClassSessionResponseObject, error) {
	return nil, httpx.ErrNotImplemented
}

func (R8Pending) DeleteClassSession(context.Context, openapi.DeleteClassSessionRequestObject) (openapi.DeleteClassSessionResponseObject, error) {
	return nil, httpx.ErrNotImplemented
}

func (R8Pending) UpdateSessionSeries(context.Context, openapi.UpdateSessionSeriesRequestObject) (openapi.UpdateSessionSeriesResponseObject, error) {
	return nil, httpx.ErrNotImplemented
}

func (R8Pending) EndSessionSeries(context.Context, openapi.EndSessionSeriesRequestObject) (openapi.EndSessionSeriesResponseObject, error) {
	return nil, httpx.ErrNotImplemented
}

// T-R8.5

func (R8Pending) GetCalendar(context.Context, openapi.GetCalendarRequestObject) (openapi.GetCalendarResponseObject, error) {
	return nil, httpx.ErrNotImplemented
}

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
