package http

import (
	"context"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"quizzivy/gen/openapi"
	"quizzivy/internal/modules/timetable/application/query"
	"quizzivy/internal/modules/timetable/domain"
	"quizzivy/internal/platform/httpapi"
	"quizzivy/internal/platform/httpx"
)

// GetStudentWeek implements GET /app/week: the caller's own week, so it names
// no id. Each item carries its kind itself, whatever the union helpers stamp.
func (h Timetable) GetStudentWeek(ctx context.Context, request openapi.GetStudentWeekRequestObject) (openapi.GetStudentWeekResponseObject, error) {
	who, ok := httpapi.ActorFromContext(ctx)
	if h.app == nil || !ok {
		return nil, httpx.ErrNotImplemented
	}
	q := query.StudentWeek{StudentID: who.ID}
	if request.Params.Start != nil {
		start := request.Params.Start.Time
		q.Start = &start
	}
	week, err := h.app.Queries.StudentWeek.Handle(ctx, q)
	if err != nil {
		return nil, err
	}
	body, err := weekBody(week)
	if err != nil {
		return nil, err
	}
	return openapi.GetStudentWeek200JSONResponse(body), nil
}

func weekBody(week domain.Week) (openapi.StudentWeek, error) {
	out := openapi.StudentWeek{
		Start: openapi_types.Date{Time: week.Start}, End: openapi_types.Date{Time: week.End},
		Days: make([]openapi.StudentWeekDay, len(week.Days)),
	}
	for i, d := range week.Days {
		day := openapi.StudentWeekDay{Date: openapi_types.Date{Time: d.Date}, Items: make([]openapi.WeekItem, len(d.Items))}
		for j, item := range d.Items {
			if err := weekItem(&day.Items[j], item); err != nil {
				return openapi.StudentWeek{}, err
			}
		}
		out.Days[i] = day
	}
	return out, nil
}

func weekItem(out *openapi.WeekItem, item domain.WeekItem) error {
	if s := item.Session; s != nil {
		return out.FromWeekSessionItem(openapi.WeekSessionItem{
			Kind: openapi.WeekSessionItemKindSession, ClassId: httpapi.ParseUUID(s.ClassID), ClassName: s.ClassName,
			StartsAt: s.StartsAt.UTC(), DurationMinutes: s.DurationMinutes, Room: s.Room, Note: s.Note, Cancelled: s.Cancelled,
		})
	}
	d := item.Due
	state := openapi.WeekDueItemStateNotStarted
	if d.Live {
		state = openapi.WeekDueItemStateInProgress
	}
	return out.FromWeekDueItem(openapi.WeekDueItem{
		Kind: openapi.Due, AssignmentId: httpapi.ParseUUID(d.ID), Title: d.Title,
		ClosesAt: d.ClosesAt.UTC(), State: state, DurationMinutes: d.DurationMinutes,
	})
}
