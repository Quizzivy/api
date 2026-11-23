package http

import (
	"context"
	"errors"
	"strings"

	"quizzivy/gen/openapi"
	"quizzivy/internal/modules/timetable/application/command"
	"quizzivy/internal/modules/timetable/application/query"
	"quizzivy/internal/modules/timetable/domain"
	"quizzivy/internal/platform/httpapi"
	"quizzivy/internal/platform/httpx"
)

func statusBody(s *domain.Status) *openapi.AttendanceStatus {
	if s == nil {
		return nil
	}
	out := openapi.AttendanceStatus(*s)
	return &out
}

func registerBody(r domain.Register) openapi.SessionAttendance {
	out := openapi.SessionAttendance{
		Session: SessionBody(r.Session), RateScope: openapi.SessionAttendanceRateScopeOverall,
		Students: make([]openapi.AttendanceStudent, len(r.Students)),
	}
	if r.Term != nil {
		out.RateScope = openapi.SessionAttendanceRateScopeTerm
		out.Term = &struct {
			Id   openapi.Uuid     `json:"id"`
			Name openapi.TermName `json:"name"`
		}{Id: httpapi.ParseUUID(r.Term.ID), Name: r.Term.Name}
	}
	counts := r.Counts()
	out.Counts.Present, out.Counts.Late, out.Counts.Absent, out.Counts.Unmarked = counts.Present, counts.Late, counts.Absent, counts.Unmarked
	for i, s := range r.Students {
		out.Students[i] = openapi.AttendanceStudent{
			StudentId: httpapi.ParseUUID(s.StudentID), FullName: s.FullName, AvatarUrl: s.AvatarURL,
			Status: statusBody(s.Status), Note: s.Note, Rate: s.Tally.Rate(),
		}
	}
	return out
}

func listedBody(s domain.ListedSession) openapi.AttendanceSessionRow {
	out := openapi.AttendanceSessionRow{
		Id: s.ID.String(), ClassId: httpapi.ParseUUID(s.ClassID), ClassName: s.ClassName, Room: s.Room,
		StartsAt: s.StartsAt.UTC(), Marked: s.HasAttendance, StudentCount: s.StudentCount,
	}
	if s.HasAttendance && s.Counts != nil {
		out.Counts = &openapi.AttendanceCounts{Present: s.Counts.Present, Late: s.Counts.Late, Absent: s.Counts.Absent}
	}
	return out
}

func refusedMarks(ctx context.Context, e *domain.RefusedMarks) openapi.ErrorResponse {
	var message, detail string
	switch e.Reason {
	case domain.RefusedTwice:
		message = httpx.Text(ctx, "Mỗi học viên chỉ được nêu một lần.", "Each student can be named only once.")
		detail = httpx.Text(ctx, "Được nêu hai lần: ", "Named twice: ")
	case domain.RefusedNoteWithoutStatus:
		message = httpx.Text(ctx, "Chỉ ghi chú được cho học viên đã điểm danh.", "A note needs a mark beside it.")
		detail = httpx.Text(ctx, "Có ghi chú nhưng chưa điểm danh: ", "A note without a mark: ")
	case domain.RefusedNoteTooLong:
		message = httpx.Text(ctx, "Ghi chú không được dài quá 500 ký tự.", "A note cannot be longer than 500 characters.")
		detail = httpx.Text(ctx, "Ghi chú quá dài: ", "Note too long: ")
	default:
		message = httpx.Text(ctx, "Có học viên không thuộc lớp này.", "Some students are not in this class.")
		detail = httpx.Text(ctx, "Không thuộc lớp này: ", "Not in this class: ")
	}
	return httpapi.ErrorWithDetails(ctx, openapi.VALIDATIONFAILED, message,
		map[string]interface{}{"studentIds": detail + strings.Join(e.StudentIDs, ", ")})
}

// ListAttendanceSessions implements GET /teacher/attendance/sessions.
func (h Timetable) ListAttendanceSessions(ctx context.Context, request openapi.ListAttendanceSessionsRequestObject) (openapi.ListAttendanceSessionsResponseObject, error) {
	who, ok := httpapi.ActorFromContext(ctx)
	if h.app == nil || !ok {
		return nil, httpx.ErrNotImplemented
	}
	p := request.Params
	q := query.AttendanceSessions{Scope: who.Scope, Filter: domain.SessionFilter{Since: p.Since, Before: p.Before, Marked: p.Marked}}
	if p.Limit != nil {
		q.Filter.Limit = *p.Limit
	}
	if p.ClassId != nil {
		class := p.ClassId.String()
		q.ClassID = &class
	}
	page, err := h.app.Queries.AttendanceSessions.Handle(ctx, q)
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return openapi.ListAttendanceSessions404JSONResponse{NotFoundJSONResponse: notFound(ctx)}, nil
	case err != nil:
		return nil, err
	}
	out := openapi.ListAttendanceSessions200JSONResponse{Items: make([]openapi.AttendanceSessionRow, len(page.Items)), NotMarked: page.NotMarked}
	for i, item := range page.Items {
		out.Items[i] = listedBody(item)
	}
	if page.NextBefore != nil {
		next := page.NextBefore.UTC()
		out.NextBefore = &next
	}
	return out, nil
}

// GetSessionAttendance implements GET /teacher/sessions/{sessionId}/attendance.
func (h Timetable) GetSessionAttendance(ctx context.Context, request openapi.GetSessionAttendanceRequestObject) (openapi.GetSessionAttendanceResponseObject, error) {
	who, ok := httpapi.ActorFromContext(ctx)
	if h.app == nil || !ok {
		return nil, httpx.ErrNotImplemented
	}
	register, err := h.app.Queries.SessionAttendance.Handle(ctx, query.SessionAttendance{Scope: who.Scope, ViewerID: who.ID, SessionID: request.SessionId})
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return openapi.GetSessionAttendance404JSONResponse{NotFoundJSONResponse: notFound(ctx)}, nil
	case err != nil:
		return nil, err
	}
	return openapi.GetSessionAttendance200JSONResponse(registerBody(register)), nil
}

// SaveSessionAttendance implements PUT /teacher/sessions/{sessionId}/attendance.
func (h Timetable) SaveSessionAttendance(ctx context.Context, request openapi.SaveSessionAttendanceRequestObject) (openapi.SaveSessionAttendanceResponseObject, error) {
	who, ok := httpapi.ActorFromContext(ctx)
	if h.app == nil || !ok || request.Body == nil {
		return nil, httpx.ErrNotImplemented
	}
	marks := make([]domain.MarkInput, len(request.Body.Marks))
	for i, m := range request.Body.Marks {
		note, err := textPatch(m.Note)
		if err != nil {
			return nil, err
		}
		marks[i] = domain.MarkInput{StudentID: m.StudentId.String(), Note: note}
		if m.Status != nil {
			status := domain.Status(*m.Status)
			marks[i].Status = &status
		}
	}
	saved, err := h.app.Commands.SaveAttendance.Handle(ctx, command.SaveAttendance{Actor: who, SessionID: request.SessionId, Marks: marks})
	var refused *domain.RefusedMarks
	switch {
	case err == nil:
		return openapi.SaveSessionAttendance200JSONResponse(registerBody(saved)), nil
	case errors.As(err, &refused):
		return openapi.SaveSessionAttendance422JSONResponse(refusedMarks(ctx, refused)), nil
	case errors.Is(err, domain.ErrNotFound):
		return openapi.SaveSessionAttendance404JSONResponse{NotFoundJSONResponse: notFound(ctx)}, nil
	case errors.Is(err, domain.ErrCancelled):
		return openapi.SaveSessionAttendance409JSONResponse(httpapi.Error(ctx, openapi.SESSIONCANCELLED,
			httpx.Text(ctx, "Buổi học đã huỷ nên không điểm danh được. Hãy khôi phục buổi học trước.", "The session is cancelled, so it takes no attendance. Restore it first."))), nil
	case errors.Is(err, domain.ErrNotStarted):
		return openapi.SaveSessionAttendance422JSONResponse(httpapi.Error(ctx, openapi.SESSIONNOTSTARTED,
			httpx.Text(ctx, "Buổi học chưa bắt đầu: chỉ điểm danh được từ 15 phút trước giờ học.", "The session has not started: attendance opens 15 minutes before it begins."))), nil
	default:
		return nil, err
	}
}
