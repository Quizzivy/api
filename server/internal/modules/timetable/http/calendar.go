package http

import (
	"context"
	"errors"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"quizzivy/gen/openapi"
	"quizzivy/internal/modules/timetable/application/query"
	"quizzivy/internal/modules/timetable/domain"
	"quizzivy/internal/platform/httpapi"
	"quizzivy/internal/platform/httpx"
	"quizzivy/internal/shared/validation"
)

// GetCalendar implements GET /teacher/calendar.
func (h Timetable) GetCalendar(ctx context.Context, request openapi.GetCalendarRequestObject) (openapi.GetCalendarResponseObject, error) {
	who, ok := httpapi.ActorFromContext(ctx)
	if h.app == nil || !ok {
		return nil, httpx.ErrNotImplemented
	}
	q := query.Calendar{Scope: who.Scope, ViewerID: who.ID, From: request.Params.From.Time, To: request.Params.To.Time}
	if request.Params.ClassId != nil {
		q.ClassID = request.Params.ClassId.String()
	}
	view, err := h.app.Queries.Calendar.Handle(ctx, q)
	var refused *validation.Error
	switch {
	case err == nil:
		return openapi.GetCalendar200JSONResponse(calendarBody(view)), nil
	case errors.As(err, &refused):
		return openapi.GetCalendar400JSONResponse{BadRequestJSONResponse: openapi.BadRequestJSONResponse(invalid(ctx, refused))}, nil
	case errors.Is(err, domain.ErrNotFound):
		return openapi.GetCalendar404JSONResponse{NotFoundJSONResponse: notFound(ctx)}, nil
	default:
		return nil, err
	}
}

func calendarBody(view query.CalendarView) openapi.Calendar {
	out := openapi.Calendar{
		Sessions:         make([]openapi.ClassSession, len(view.Sessions)),
		AssignmentEvents: make([]openapi.CalendarAssignmentEvent, len(view.Events)),
		Terms:            make([]openapi.CalendarTerm, len(view.Terms)),
	}
	for i, s := range view.Sessions {
		out.Sessions[i] = SessionBody(s)
	}
	for i, e := range view.Events {
		out.AssignmentEvents[i] = openapi.CalendarAssignmentEvent{
			Kind: openapi.CalendarAssignmentEventKind(e.Kind), At: e.At.UTC(), AssignmentId: httpapi.ParseUUID(e.AssignmentID),
			Title: e.Title, ClassNames: append([]string{}, e.ClassNames...),
		}
	}
	for i, t := range view.Terms {
		out.Terms[i] = openapi.CalendarTerm{
			Id: httpapi.ParseUUID(t.ID), Name: t.Name,
			StartsOn: openapi_types.Date{Time: t.StartsOn}, EndsOn: openapi_types.Date{Time: t.EndsOn},
		}
	}
	return out
}
