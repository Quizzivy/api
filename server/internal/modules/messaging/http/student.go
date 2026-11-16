package http

import (
	"context"
	"errors"

	"quizzivy/gen/openapi"
	"quizzivy/internal/modules/messaging/application"
	"quizzivy/internal/modules/messaging/application/command"
	"quizzivy/internal/modules/messaging/application/query"
	"quizzivy/internal/modules/messaging/domain"
	"quizzivy/internal/platform/httpapi"
	"quizzivy/internal/platform/httpx"
)

// Student serves the student side of messaging, under /app/messages: the
// student's Messages, a direct thread addressed by its teacher, and a class's
// channel, which a student reads and never posts in. Every answer is the
// student's own shape (StudentMessage), never a staff one handed on.
type Student struct {
	app *application.Application
}

// NewStudent builds the transport. A nil application answers 501.
func NewStudent(app *application.Application) Student { return Student{app: app} }

func teacherNotAllowed(ctx context.Context) openapi.ErrorResponse {
	return httpapi.Error(ctx, openapi.MESSAGINGNOTALLOWED,
		httpx.Text(ctx, "Không thể nhắn cho giáo viên này lúc này.", "You cannot message this teacher right now."))
}

func studentMessageOf(m domain.StudentMessage) openapi.StudentMessage {
	out := openapi.StudentMessage{
		Id: httpapi.ParseUUID(m.ID), Body: m.Body, CreatedAt: m.CreatedAt.UTC(), Mine: m.Mine,
		Sender: senderOf(m.Sender),
	}
	if a := m.Attachment; a != nil {
		out.Attachment = &openapi.MessageAttachment{
			Id: httpapi.ParseUUID(a.ID), Filename: a.Filename, Bytes: a.Bytes, MimeType: openapi.MessageAttachmentMimeType(a.MimeType),
		}
	}
	return out
}

func senderOf(s domain.Sender) openapi.MessageSender {
	return openapi.MessageSender{Id: httpapi.ParseUUID(s.ID), Name: s.Name, AvatarUrl: s.AvatarURL}
}

func studentMessagesOf(page domain.StudentPage) []openapi.StudentMessage {
	out := make([]openapi.StudentMessage, len(page.Messages))
	for i, m := range page.Messages {
		out[i] = studentMessageOf(m)
	}
	return out
}

func studentThreadOf(t domain.StudentThread) openapi.StudentThread {
	ref := openapi.StudentThreadRef{Kind: openapi.StudentThreadRefKind(t.Kind)}
	if t.ClassID != "" {
		ref.ClassId = new(httpapi.RawUUID(t.ClassID))
	}
	if t.TeacherID != "" {
		ref.TeacherId = new(httpapi.RawUUID(t.TeacherID))
	}
	out := openapi.StudentThread{
		Thread: ref, Title: t.Title, AvatarUrl: t.AvatarURL, ClassNames: t.ClassNames, Former: t.Former, Unread: t.Unread,
	}
	if t.LastMessageAt != nil {
		out.LastMessageAt = new(t.LastMessageAt.UTC())
	}
	if p := t.Preview; p != nil {
		out.Preview = &openapi.MessagePreview{Text: p.Text, AttachmentName: p.AttachmentName, Mine: p.Mine}
	}
	return out
}

// ListStudentThreads implements GET /app/messages/threads.
func (h Student) ListStudentThreads(ctx context.Context, _ openapi.ListStudentThreadsRequestObject) (openapi.ListStudentThreadsResponseObject, error) {
	who, ok := httpapi.ActorFromContext(ctx)
	if h.app == nil || !ok {
		return nil, httpx.ErrNotImplemented
	}
	threads, err := h.app.Queries.StudentThreads.Handle(ctx, query.StudentThreads{StudentID: who.ID})
	if err != nil {
		return nil, err
	}
	out := openapi.ListStudentThreads200JSONResponse{Threads: make([]openapi.StudentThread, len(threads))}
	for i, t := range threads {
		out.Threads[i] = studentThreadOf(t)
	}
	return out, nil
}

// GetStudentDirectThread implements GET /app/messages/teachers/{teacherId}.
func (h Student) GetStudentDirectThread(ctx context.Context, request openapi.GetStudentDirectThreadRequestObject) (openapi.GetStudentDirectThreadResponseObject, error) {
	who, ok := httpapi.ActorFromContext(ctx)
	if h.app == nil || !ok {
		return nil, httpx.ErrNotImplemented
	}
	p := request.Params
	thread, err := h.app.Queries.StudentDirectThread.Handle(ctx, query.StudentDirectThread{
		StudentID: who.ID, TeacherID: request.TeacherId.String(), Cursor: cursorOf(p.Before, p.After, p.Limit),
	})
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return openapi.GetStudentDirectThread404JSONResponse{NotFoundJSONResponse: threadNotFound(ctx)}, nil
	case errors.Is(err, domain.ErrInvalid):
		return openapi.GetStudentDirectThread400JSONResponse{BadRequestJSONResponse: badRequest(ctx)}, nil
	case err != nil:
		return nil, err
	}
	t := thread.Teacher
	return openapi.GetStudentDirectThread200JSONResponse{
		Teacher: openapi.StudentDirectThreadTeacher{
			Id: httpapi.ParseUUID(t.ID), Name: t.Name, AvatarUrl: t.AvatarURL, ClassNames: t.ClassNames, Former: t.Former, CanPost: t.CanPost,
		},
		Messages:   studentMessagesOf(thread.Page),
		NextBefore: uuidOf(thread.Page.NextBefore),
		LatestId:   uuidOf(thread.Page.LatestID),
	}, nil
}

// PostStudentDirectMessage implements POST /app/messages/teachers/{teacherId}.
func (h Student) PostStudentDirectMessage(ctx context.Context, request openapi.PostStudentDirectMessageRequestObject) (openapi.PostStudentDirectMessageResponseObject, error) {
	who, ok := httpapi.ActorFromContext(ctx)
	if h.app == nil || !ok || request.Body == nil {
		return nil, httpx.ErrNotImplemented
	}
	in := *request.Body
	posted, err := h.app.Commands.PostAsStudent.Handle(ctx, command.PostAsStudent{
		StudentID: who.ID, TeacherID: request.TeacherId.String(), ClientID: in.ClientId.String(), Body: in.Body,
	})
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return openapi.PostStudentDirectMessage404JSONResponse{NotFoundJSONResponse: threadNotFound(ctx)}, nil
	case errors.Is(err, domain.ErrNotAllowed):
		return openapi.PostStudentDirectMessage409JSONResponse(teacherNotAllowed(ctx)), nil
	case errors.Is(err, domain.ErrEmpty):
		return openapi.PostStudentDirectMessage422JSONResponse(emptyText(ctx)), nil
	case errors.Is(err, domain.ErrClientIDTaken):
		return openapi.PostStudentDirectMessage422JSONResponse(clientIDTaken(ctx)), nil
	case errors.Is(err, domain.ErrInvalid):
		return openapi.PostStudentDirectMessage400JSONResponse{BadRequestJSONResponse: badRequest(ctx)}, nil
	case err != nil:
		return nil, err
	}
	if !posted.Created {
		return openapi.PostStudentDirectMessage200JSONResponse(studentMessageOf(posted.Message)), nil
	}
	return openapi.PostStudentDirectMessage201JSONResponse(studentMessageOf(posted.Message)), nil
}

func emptyText(ctx context.Context) openapi.ErrorResponse {
	return httpapi.FieldError(ctx, "body", httpx.Text(ctx, "Tin nhắn cần có nội dung.", "A message needs text."))
}

// MarkStudentDirectThreadRead implements POST /app/messages/teachers/{teacherId}/read.
func (h Student) MarkStudentDirectThreadRead(ctx context.Context, request openapi.MarkStudentDirectThreadReadRequestObject) (openapi.MarkStudentDirectThreadReadResponseObject, error) {
	who, ok := httpapi.ActorFromContext(ctx)
	if h.app == nil || !ok || request.Body == nil {
		return nil, httpx.ErrNotImplemented
	}
	_, err := h.app.Commands.MarkReadAsStudent.Handle(ctx, command.MarkReadAsStudent{
		StudentID: who.ID, TeacherID: request.TeacherId.String(), MessageID: request.Body.LastReadMessageId.String(),
	})
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return openapi.MarkStudentDirectThreadRead404JSONResponse{NotFoundJSONResponse: threadNotFound(ctx)}, nil
	case errors.Is(err, domain.ErrNotInThread):
		return openapi.MarkStudentDirectThreadRead422JSONResponse(notInThread(ctx)), nil
	case err != nil:
		return nil, err
	}
	return openapi.MarkStudentDirectThreadRead204Response{}, nil
}

// GetStudentClassThread implements GET /app/messages/classes/{classId}. The
// path takes no POST: a class's channel is read-only for students.
func (h Student) GetStudentClassThread(ctx context.Context, request openapi.GetStudentClassThreadRequestObject) (openapi.GetStudentClassThreadResponseObject, error) {
	who, ok := httpapi.ActorFromContext(ctx)
	if h.app == nil || !ok {
		return nil, httpx.ErrNotImplemented
	}
	p := request.Params
	thread, err := h.app.Queries.StudentClassThread.Handle(ctx, query.StudentClassThread{
		StudentID: who.ID, ClassID: request.ClassId.String(), Cursor: cursorOf(p.Before, p.After, p.Limit),
	})
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return openapi.GetStudentClassThread404JSONResponse{NotFoundJSONResponse: threadNotFound(ctx)}, nil
	case errors.Is(err, domain.ErrInvalid):
		return openapi.GetStudentClassThread400JSONResponse{BadRequestJSONResponse: badRequest(ctx)}, nil
	case err != nil:
		return nil, err
	}
	c := thread.Channel
	return openapi.GetStudentClassThread200JSONResponse{
		ReadOnly:   true,
		Class:      openapi.PersonRef{Id: httpapi.ParseUUID(c.ClassID), Name: c.Name},
		Teacher:    senderOf(c.Teacher),
		Messages:   studentMessagesOf(thread.Page),
		NextBefore: uuidOf(thread.Page.NextBefore),
		LatestId:   uuidOf(thread.Page.LatestID),
	}, nil
}

// MarkStudentClassThreadRead implements POST /app/messages/classes/{classId}/read.
func (h Student) MarkStudentClassThreadRead(ctx context.Context, request openapi.MarkStudentClassThreadReadRequestObject) (openapi.MarkStudentClassThreadReadResponseObject, error) {
	who, ok := httpapi.ActorFromContext(ctx)
	if h.app == nil || !ok || request.Body == nil {
		return nil, httpx.ErrNotImplemented
	}
	_, err := h.app.Commands.MarkReadAsStudent.Handle(ctx, command.MarkReadAsStudent{
		StudentID: who.ID, ClassID: request.ClassId.String(), MessageID: request.Body.LastReadMessageId.String(),
	})
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return openapi.MarkStudentClassThreadRead404JSONResponse{NotFoundJSONResponse: threadNotFound(ctx)}, nil
	case errors.Is(err, domain.ErrNotInThread):
		return openapi.MarkStudentClassThreadRead422JSONResponse(notInThread(ctx)), nil
	case err != nil:
		return nil, err
	}
	return openapi.MarkStudentClassThreadRead204Response{}, nil
}
