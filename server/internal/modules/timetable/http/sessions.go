// Package http is the timetable's transport: a class's sessions under
// /teacher/classes/{id}/sessions, one session under /teacher/sessions, a
// weekly series under /teacher/session-series and the calendar under
// /teacher/calendar.
package http

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"quizzivy/gen/openapi"
	"quizzivy/internal/modules/timetable/application"
	"quizzivy/internal/modules/timetable/application/command"
	"quizzivy/internal/modules/timetable/domain"
	"quizzivy/internal/platform/httpapi"
	"quizzivy/internal/platform/httpx"
	"quizzivy/internal/shared/validation"
)

// Timetable serves the session and series operations.
type Timetable struct {
	app *application.Application
}

// NewTimetable builds the transport. A nil application answers 501.
func NewTimetable(app *application.Application) Timetable { return Timetable{app: app} }

func notFound(ctx context.Context) openapi.NotFoundJSONResponse {
	return openapi.NotFoundJSONResponse(httpapi.NotFound(ctx,
		httpx.Text(ctx, "Không tìm thấy lớp, buổi học hoặc lịch học này.", "This class, session or weekly session was not found.")))
}

func invalid(ctx context.Context, refused *validation.Error) openapi.ErrorResponse {
	return httpapi.Invalid(ctx, httpx.Text(ctx, "Buổi học không hợp lệ.", "The session is not valid."), refused)
}

func hasAttendance(ctx context.Context) openapi.ErrorResponse {
	return httpapi.Error(ctx, openapi.SESSIONHASATTENDANCE,
		httpx.Text(ctx, "Buổi học này đã có điểm danh, nên chỉ có thể huỷ.", "A student is marked on this session, so it can only be cancelled."))
}

func textPatch(raw json.RawMessage) (domain.TextPatch, error) {
	if len(raw) == 0 {
		return domain.TextPatch{}, nil
	}
	var value *string
	if err := json.Unmarshal(raw, &value); err != nil {
		return domain.TextPatch{}, err
	}
	return domain.TextPatch{Set: true, Value: value}, nil
}

func clock(field, raw string) (domain.Clock, error) {
	c, err := domain.ParseClock(raw)
	if err != nil {
		return domain.Clock{}, validation.Refuse(field, "Giờ không hợp lệ.", "The time is not valid.")
	}
	return c, nil
}

func dayOf(d *openapi_types.Date) *time.Time {
	if d == nil {
		return nil
	}
	return &d.Time
}

func seriesBody(s domain.Series) openapi.SessionSeries {
	out := openapi.SessionSeries{
		Id: httpapi.ParseUUID(s.ID), ClassId: httpapi.ParseUUID(s.ClassID), Weekday: int(s.Weekday), StartTime: s.Start.String(),
		DurationMinutes: s.DurationMinutes, Room: s.Room, StartsOn: openapi_types.Date{Time: s.StartsOn}, TimeZone: s.TimeZone,
	}
	if s.EndsOn != nil {
		out.EndsOn = &openapi_types.Date{Time: *s.EndsOn}
	}
	return out
}

// SessionBody is s as the contract's ClassSession.
func SessionBody(s domain.Session) openapi.ClassSession {
	out := openapi.ClassSession{
		Id: s.ID.String(), ClassId: httpapi.ParseUUID(s.ClassID), ClassName: s.ClassName,
		Date: openapi_types.Date{Time: s.Date}, StartsAt: s.StartsAt.UTC(), DurationMinutes: s.DurationMinutes,
		Room: s.Room, Note: s.Note, Cancelled: s.Cancelled, Changed: s.Changed, HasAttendance: s.HasAttendance,
	}
	if s.Series != nil {
		series := seriesBody(*s.Series)
		out.Series, out.SeriesId = &series, &series.Id
	}
	return out
}

// CreateClassSession implements POST /teacher/classes/{id}/sessions.
func (h Timetable) CreateClassSession(ctx context.Context, request openapi.CreateClassSessionRequestObject) (openapi.CreateClassSessionResponseObject, error) {
	who, ok := httpapi.ActorFromContext(ctx)
	if h.app == nil || !ok || request.Body == nil {
		return nil, httpx.ErrNotImplemented
	}
	created, err := h.create(ctx, who, request)
	if err == nil {
		return openapi.CreateClassSession201JSONResponse(SessionBody(created)), nil
	}
	var refused *validation.Error
	switch {
	case errors.As(err, &refused):
		return openapi.CreateClassSession400JSONResponse(invalid(ctx, refused)), nil
	case errors.Is(err, domain.ErrNotFound):
		return openapi.CreateClassSession404JSONResponse{NotFoundJSONResponse: notFound(ctx)}, nil
	case errors.Is(err, domain.ErrClassArchived):
		return openapi.CreateClassSession409JSONResponse(httpapi.Error(ctx, openapi.CLASSARCHIVED,
			httpx.Text(ctx, "Lớp đã lưu trữ nên không thêm buổi học được.", "The class is archived, so it takes no new session."))), nil
	default:
		return nil, err
	}
}

// UpdateClassSession implements PATCH /teacher/sessions/{sessionId}.
func (h Timetable) UpdateClassSession(ctx context.Context, request openapi.UpdateClassSessionRequestObject) (openapi.UpdateClassSessionResponseObject, error) {
	who, ok := httpapi.ActorFromContext(ctx)
	if h.app == nil || !ok || request.Body == nil {
		return nil, httpx.ErrNotImplemented
	}
	saved, err := h.update(ctx, who, request)
	if err == nil {
		return openapi.UpdateClassSession200JSONResponse(SessionBody(saved)), nil
	}
	var refused *validation.Error
	switch {
	case errors.As(err, &refused):
		return openapi.UpdateClassSession400JSONResponse{BadRequestJSONResponse: openapi.BadRequestJSONResponse(invalid(ctx, refused))}, nil
	case errors.Is(err, domain.ErrNotFound):
		return openapi.UpdateClassSession404JSONResponse{NotFoundJSONResponse: notFound(ctx)}, nil
	default:
		return nil, err
	}
}

// DeleteClassSession implements DELETE /teacher/sessions/{sessionId}.
func (h Timetable) DeleteClassSession(ctx context.Context, request openapi.DeleteClassSessionRequestObject) (openapi.DeleteClassSessionResponseObject, error) {
	who, ok := httpapi.ActorFromContext(ctx)
	if h.app == nil || !ok {
		return nil, httpx.ErrNotImplemented
	}
	_, err := h.app.Commands.DeleteSession.Handle(ctx, command.DeleteSession{Actor: who, SessionID: request.SessionId})
	switch {
	case err == nil:
		return openapi.DeleteClassSession204Response{}, nil
	case errors.Is(err, domain.ErrNotFound):
		return openapi.DeleteClassSession404JSONResponse{NotFoundJSONResponse: notFound(ctx)}, nil
	case errors.Is(err, domain.ErrInSeries):
		return openapi.DeleteClassSession409JSONResponse(httpapi.Error(ctx, openapi.SESSIONINSERIES,
			httpx.Text(ctx, "Đây là một buổi của lịch học hằng tuần: hãy huỷ buổi này.", "This is one date of a weekly session: cancel it instead."))), nil
	case errors.Is(err, domain.ErrHasAttendance):
		return openapi.DeleteClassSession409JSONResponse(hasAttendance(ctx)), nil
	default:
		return nil, err
	}
}

// UpdateSessionSeries implements PATCH /teacher/session-series/{seriesId}.
func (h Timetable) UpdateSessionSeries(ctx context.Context, request openapi.UpdateSessionSeriesRequestObject) (openapi.UpdateSessionSeriesResponseObject, error) {
	who, ok := httpapi.ActorFromContext(ctx)
	if h.app == nil || !ok || request.Body == nil {
		return nil, httpx.ErrNotImplemented
	}
	edited, err := h.updateSeries(ctx, who, request)
	if err == nil {
		return openapi.UpdateSessionSeries200JSONResponse{Series: seriesBody(edited.Series), DroppedChanges: edited.Dropped}, nil
	}
	var refused *validation.Error
	switch {
	case errors.As(err, &refused):
		return openapi.UpdateSessionSeries400JSONResponse(invalid(ctx, refused)), nil
	case errors.Is(err, domain.ErrNotFound):
		return openapi.UpdateSessionSeries404JSONResponse{NotFoundJSONResponse: notFound(ctx)}, nil
	default:
		return nil, err
	}
}

// EndSessionSeries implements POST /teacher/session-series/{seriesId}/end.
func (h Timetable) EndSessionSeries(ctx context.Context, request openapi.EndSessionSeriesRequestObject) (openapi.EndSessionSeriesResponseObject, error) {
	who, ok := httpapi.ActorFromContext(ctx)
	if h.app == nil || !ok || request.Body == nil {
		return nil, httpx.ErrNotImplemented
	}
	ended, err := h.app.Commands.EndSeries.Handle(ctx, command.EndSeries{Actor: who, SeriesID: request.SeriesId.String(), LastDate: request.Body.LastDate.Time})
	var refused *validation.Error
	switch {
	case err == nil && ended == nil:
		return openapi.EndSessionSeries204Response{}, nil
	case err == nil:
		return openapi.EndSessionSeries200JSONResponse(seriesBody(*ended)), nil
	case errors.As(err, &refused):
		return openapi.EndSessionSeries400JSONResponse(invalid(ctx, refused)), nil
	case errors.Is(err, domain.ErrNotFound):
		return openapi.EndSessionSeries404JSONResponse{NotFoundJSONResponse: notFound(ctx)}, nil
	case errors.Is(err, domain.ErrHasAttendance):
		return openapi.EndSessionSeries409JSONResponse(httpapi.Error(ctx, openapi.SESSIONHASATTENDANCE,
			httpx.Text(ctx, "Một buổi sau ngày này đã có điểm danh, nên lịch học chưa thể dừng.", "A session after this date has attendance, so the weekly session cannot stop before it."))), nil
	default:
		return nil, err
	}
}

func (h Timetable) create(ctx context.Context, who httpapi.Actor, request openapi.CreateClassSessionRequestObject) (domain.Session, error) {
	b := request.Body
	draft := domain.Draft{Date: b.Date.Time, DurationMinutes: b.DurationMinutes, RepeatWeekly: b.RepeatWeekly, EndsOn: dayOf(b.EndsOn)}
	var err error
	if draft.Room, err = textPatch(b.Room); err != nil {
		return domain.Session{}, err
	}
	if draft.Start, err = clock("startTime", b.StartTime); err != nil {
		return domain.Session{}, err
	}
	return h.app.Commands.CreateSession.Handle(ctx, command.CreateSession{Actor: who, ClassID: request.Id.String(), Draft: draft})
}

func (h Timetable) update(ctx context.Context, who httpapi.Actor, request openapi.UpdateClassSessionRequestObject) (domain.Session, error) {
	b := request.Body
	patch := domain.Patch{Date: dayOf(b.Date), DurationMinutes: b.DurationMinutes, Cancelled: b.Cancelled}
	var err error
	if patch.Room, err = textPatch(b.Room); err != nil {
		return domain.Session{}, err
	}
	if patch.Note, err = textPatch(b.Note); err != nil {
		return domain.Session{}, err
	}
	if b.StartTime != nil {
		start, err := clock("startTime", *b.StartTime)
		if err != nil {
			return domain.Session{}, err
		}
		patch.Start = &start
	}
	return h.app.Commands.UpdateSession.Handle(ctx, command.UpdateSession{Actor: who, SessionID: request.SessionId, Patch: patch})
}

func (h Timetable) updateSeries(ctx context.Context, who httpapi.Actor, request openapi.UpdateSessionSeriesRequestObject) (domain.SeriesEdit, error) {
	b := request.Body
	change := domain.SeriesChange{DurationMinutes: b.DurationMinutes}
	var err error
	if change.Room, err = textPatch(b.Room); err != nil {
		return domain.SeriesEdit{}, err
	}
	if b.Weekday != nil {
		weekday := domain.Weekday(*b.Weekday)
		change.Weekday = &weekday
	}
	if b.StartTime != nil {
		start, err := clock("startTime", *b.StartTime)
		if err != nil {
			return domain.SeriesEdit{}, err
		}
		change.Start = &start
	}
	return h.app.Commands.UpdateSeries.Handle(ctx, command.UpdateSeries{
		Actor: who, SeriesID: request.SeriesId.String(), FromDate: b.FromDate.Time, Change: change,
	})
}
