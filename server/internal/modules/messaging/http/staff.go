// Package http is the messaging context's transport. The staff side lives
// under /teacher/messages: the Messages list, a direct thread addressed by its
// student, and a class's announcement channel addressed by its class. The
// student side lives under /app/messages (student.go).
package http

import (
	"context"
	"errors"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"quizzivy/gen/openapi"
	"quizzivy/internal/modules/messaging/application"
	"quizzivy/internal/modules/messaging/application/command"
	"quizzivy/internal/modules/messaging/application/query"
	"quizzivy/internal/modules/messaging/domain"
	"quizzivy/internal/platform/httpapi"
	"quizzivy/internal/platform/httpx"
	"quizzivy/internal/shared/access"
)

// Staff serves the staff side of messaging.
type Staff struct {
	app *application.Application
}

// NewStaff builds the transport. A nil application answers 501.
func NewStaff(app *application.Application) Staff { return Staff{app: app} }

func threadNotFound(ctx context.Context) openapi.NotFoundJSONResponse {
	return openapi.NotFoundJSONResponse(httpapi.NotFound(ctx,
		httpx.Text(ctx, "Không tìm thấy cuộc trò chuyện này.", "This conversation was not found.")))
}

func attachmentNotFound(ctx context.Context) openapi.NotFoundJSONResponse {
	return openapi.NotFoundJSONResponse(httpapi.NotFound(ctx,
		httpx.Text(ctx, "Không tìm thấy tệp đính kèm này, hoặc tệp đã được gửi.", "This attachment was not found, or it was already sent.")))
}

func badRequest(ctx context.Context) openapi.BadRequestJSONResponse {
	return openapi.BadRequestJSONResponse(httpapi.Error(ctx, openapi.VALIDATIONFAILED,
		httpx.Text(ctx, "Yêu cầu không hợp lệ: chỉ dùng before hoặc after, không dùng cả hai.", "The request is not valid: use before or after, not both.")))
}

func emptyMessage(ctx context.Context) openapi.ErrorResponse {
	return httpapi.FieldError(ctx, "body", httpx.Text(ctx, "Tin nhắn cần có nội dung hoặc tệp đính kèm.", "A message needs text or a file."))
}

func clientIDTaken(ctx context.Context) openapi.ErrorResponse {
	return httpapi.FieldError(ctx, "clientId", httpx.Text(ctx, "Mã clientId này đã dùng cho một tin nhắn ở cuộc trò chuyện khác.", "This clientId was already used for a message in another conversation."))
}

func notInThread(ctx context.Context) openapi.ErrorResponse {
	return httpapi.FieldError(ctx, "lastReadMessageId", httpx.Text(ctx, "Tin nhắn này không thuộc cuộc trò chuyện.", "This message is not in this conversation."))
}

func notAllowed(ctx context.Context) openapi.ErrorResponse {
	return httpapi.Error(ctx, openapi.MESSAGINGNOTALLOWED,
		httpx.Text(ctx, "Không thể nhắn cho học viên này: tài khoản đã bị khoá hoặc không còn là học viên.", "You cannot message this student: the account is disabled or no longer a student."))
}

func classArchived(ctx context.Context) openapi.ErrorResponse {
	return httpapi.Error(ctx, openapi.CLASSARCHIVED,
		httpx.Text(ctx, "Lớp đã lưu trữ. Khôi phục lớp trước khi đăng thông báo.", "The class is archived. Restore it before posting an announcement."))
}

func forbidden(ctx context.Context) openapi.ForbiddenJSONResponse {
	return openapi.ForbiddenJSONResponse(httpapi.Error(ctx, openapi.FORBIDDEN,
		httpx.Text(ctx, "Bạn không có quyền đăng thông báo cho lớp.", "You may not post announcements to classes.")))
}

func cursorOf(before, after *openapi.Uuid, limit *int) domain.Cursor {
	var c domain.Cursor
	if before != nil {
		c.Before = before.String()
	}
	if after != nil {
		c.After = after.String()
	}
	if limit != nil {
		c.Limit = *limit
	}
	return c
}

func uuidOf(s *string) *openapi_types.UUID {
	if s == nil {
		return nil
	}
	id := httpapi.RawUUID(*s)
	return &id
}

func stringOf(id *openapi_types.UUID) *string {
	if id == nil {
		return nil
	}
	s := id.String()
	return &s
}

func messageOf(m domain.Message) openapi.StaffMessage {
	out := openapi.StaffMessage{
		Id: httpapi.ParseUUID(m.ID), Body: m.Body, CreatedAt: m.CreatedAt.UTC(), Mine: m.Mine,
		Sender: openapi.StaffMessageSender{
			Id: httpapi.ParseUUID(m.SenderID), Name: m.SenderName, AvatarUrl: m.SenderAvatarURL, Side: openapi.StaffMessageSenderSide(m.Side),
		},
	}
	if a := m.Attachment; a != nil {
		out.Attachment = &openapi.MessageAttachment{
			Id: httpapi.ParseUUID(a.ID), Filename: a.Filename, Bytes: a.Bytes, MimeType: openapi.MessageAttachmentMimeType(a.MimeType),
		}
	}
	if m.SeenBy != nil {
		out.SeenBy = &openapi.SeenBy{Seen: m.SeenBy.Seen, Of: m.SeenBy.Of}
	}
	return out
}

func messagesOf(page domain.Page) []openapi.StaffMessage {
	out := make([]openapi.StaffMessage, len(page.Messages))
	for i, m := range page.Messages {
		out[i] = messageOf(m)
	}
	return out
}

func threadOf(t domain.StaffThread) openapi.StaffThread {
	ref := openapi.StaffThreadRef{Kind: openapi.StaffThreadRefKind(t.Ref.Kind)}
	if t.Ref.ClassID != "" {
		ref.ClassId = new(httpapi.RawUUID(t.Ref.ClassID))
	}
	if t.Ref.StudentID != "" {
		ref.StudentId = new(httpapi.RawUUID(t.Ref.StudentID))
	}
	out := openapi.StaffThread{
		Thread: ref, Title: t.Title, AvatarUrl: t.AvatarURL, ClassNames: t.ClassNames, StudentCount: t.StudentCount,
		Former: t.Former, Candidate: t.Candidate, Disabled: t.Disabled, Unread: t.Unread,
	}
	if t.LastMessageAt != nil {
		out.LastMessageAt = new(t.LastMessageAt.UTC())
	}
	if p := t.Preview; p != nil {
		out.Preview = &openapi.MessagePreview{Text: p.Text, AttachmentName: p.AttachmentName, Mine: p.Mine}
	}
	return out
}

func contextEventsOf(events []domain.ContextEvent) []openapi.ContextEvent {
	out := make([]openapi.ContextEvent, len(events))
	for i, e := range events {
		out[i] = openapi.ContextEvent{
			AttemptId: httpapi.ParseUUID(e.AttemptID), AssignmentId: httpapi.ParseUUID(e.AssignmentID),
			Title: e.Title, FocusLost: e.FocusLost, At: e.At.UTC(),
		}
	}
	return out
}

func mayWriteClasses(ctx context.Context) bool {
	principal, _ := httpx.PrincipalFromContext(ctx)
	return principal.Access.Permissions.Has(access.TeachingClassesWrite)
}

// ListStaffThreads implements GET /teacher/messages/threads.
func (h Staff) ListStaffThreads(ctx context.Context, request openapi.ListStaffThreadsRequestObject) (openapi.ListStaffThreadsResponseObject, error) {
	who, ok := httpapi.ActorFromContext(ctx)
	if h.app == nil || !ok {
		return nil, httpx.ErrNotImplemented
	}
	q := query.StaffThreads{ActorID: who.ID, All: who.Scope.All}
	if request.Params.Tab != nil {
		q.Tab = domain.Tab(*request.Params.Tab)
	}
	if request.Params.Q != nil {
		q.Query = string(*request.Params.Q)
	}
	threads, err := h.app.Queries.StaffThreads.Handle(ctx, q)
	switch {
	case errors.Is(err, domain.ErrInvalid):
		return openapi.ListStaffThreads400JSONResponse{BadRequestJSONResponse: badRequest(ctx)}, nil
	case err != nil:
		return nil, err
	}
	out := openapi.ListStaffThreads200JSONResponse{Threads: make([]openapi.StaffThread, len(threads))}
	for i, t := range threads {
		out.Threads[i] = threadOf(t)
	}
	return out, nil
}

// GetStaffDirectThread implements GET /teacher/messages/students/{studentId}.
func (h Staff) GetStaffDirectThread(ctx context.Context, request openapi.GetStaffDirectThreadRequestObject) (openapi.GetStaffDirectThreadResponseObject, error) {
	who, ok := httpapi.ActorFromContext(ctx)
	if h.app == nil || !ok {
		return nil, httpx.ErrNotImplemented
	}
	p := request.Params
	thread, err := h.app.Queries.StaffDirectThread.Handle(ctx, query.StaffDirectThread{
		ActorID: who.ID, Scope: who.Scope, StudentID: request.StudentId.String(), Cursor: cursorOf(p.Before, p.After, p.Limit),
	})
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return openapi.GetStaffDirectThread404JSONResponse{NotFoundJSONResponse: threadNotFound(ctx)}, nil
	case errors.Is(err, domain.ErrInvalid):
		return openapi.GetStaffDirectThread400JSONResponse{BadRequestJSONResponse: badRequest(ctx)}, nil
	case err != nil:
		return nil, err
	}
	peer := thread.Peer
	classes := make([]openapi.PersonRef, len(peer.Classes))
	for i, c := range peer.Classes {
		classes[i] = openapi.PersonRef{Id: httpapi.ParseUUID(c.ID), Name: c.Name}
	}
	return openapi.GetStaffDirectThread200JSONResponse{
		Student: openapi.StaffDirectThreadPeer{
			Id: httpapi.ParseUUID(peer.ID), Name: peer.Name, AvatarUrl: peer.AvatarURL, Classes: classes,
			Former: peer.Former, Disabled: peer.Disabled, CanPost: peer.CanPost,
		},
		Messages:      messagesOf(thread.Page),
		ContextEvents: contextEventsOf(thread.Events),
		NextBefore:    uuidOf(thread.Page.NextBefore),
		LatestId:      uuidOf(thread.Page.LatestID),
	}, nil
}

// PostStaffDirectMessage implements POST /teacher/messages/students/{studentId}.
func (h Staff) PostStaffDirectMessage(ctx context.Context, request openapi.PostStaffDirectMessageRequestObject) (openapi.PostStaffDirectMessageResponseObject, error) {
	who, ok := httpapi.ActorFromContext(ctx)
	if h.app == nil || !ok || request.Body == nil {
		return nil, httpx.ErrNotImplemented
	}
	in := *request.Body
	posted, err := h.app.Commands.PostDirect.Handle(ctx, command.PostDirect{
		Actor: who, StudentID: request.StudentId.String(), ClientID: in.ClientId.String(), Body: in.Body, AttachmentID: stringOf(in.AttachmentId),
	})
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return openapi.PostStaffDirectMessage404JSONResponse{NotFoundJSONResponse: threadNotFound(ctx)}, nil
	case errors.Is(err, domain.ErrAttachmentNotFound):
		return openapi.PostStaffDirectMessage404JSONResponse{NotFoundJSONResponse: attachmentNotFound(ctx)}, nil
	case errors.Is(err, domain.ErrNotAllowed):
		return openapi.PostStaffDirectMessage409JSONResponse(notAllowed(ctx)), nil
	case errors.Is(err, domain.ErrEmpty):
		return openapi.PostStaffDirectMessage422JSONResponse(emptyMessage(ctx)), nil
	case errors.Is(err, domain.ErrClientIDTaken):
		return openapi.PostStaffDirectMessage422JSONResponse(clientIDTaken(ctx)), nil
	case errors.Is(err, domain.ErrInvalid):
		return openapi.PostStaffDirectMessage400JSONResponse{BadRequestJSONResponse: badRequest(ctx)}, nil
	case err != nil:
		return nil, err
	}
	if !posted.Created {
		return openapi.PostStaffDirectMessage200JSONResponse(messageOf(posted.Message)), nil
	}
	return openapi.PostStaffDirectMessage201JSONResponse(messageOf(posted.Message)), nil
}

// MarkStaffDirectThreadRead implements POST /teacher/messages/students/{studentId}/read.
func (h Staff) MarkStaffDirectThreadRead(ctx context.Context, request openapi.MarkStaffDirectThreadReadRequestObject) (openapi.MarkStaffDirectThreadReadResponseObject, error) {
	if h.app == nil || request.Body == nil {
		return nil, httpx.ErrNotImplemented
	}
	err := h.markRead(ctx, domain.ThreadRef{Kind: domain.KindDirect, StudentID: request.StudentId.String()}, request.Body.LastReadMessageId)
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return openapi.MarkStaffDirectThreadRead404JSONResponse{NotFoundJSONResponse: threadNotFound(ctx)}, nil
	case errors.Is(err, domain.ErrNotInThread):
		return openapi.MarkStaffDirectThreadRead422JSONResponse(notInThread(ctx)), nil
	case err != nil:
		return nil, err
	}
	return openapi.MarkStaffDirectThreadRead204Response{}, nil
}

// markRead moves the caller's pointer in the thread ref names forward to
// message: domain.ErrNotFound for a thread the caller cannot read, and
// domain.ErrNotInThread for a message that is not one of its.
func (h Staff) markRead(ctx context.Context, ref domain.ThreadRef, message openapi.Uuid) error {
	who, ok := httpapi.ActorFromContext(ctx)
	if !ok {
		return httpx.ErrNotImplemented
	}
	_, err := h.app.Commands.MarkRead.Handle(ctx, command.MarkRead{Actor: who, Thread: ref, MessageID: message.String()})
	return err
}

// GetStaffClassThread implements GET /teacher/messages/classes/{classId}.
func (h Staff) GetStaffClassThread(ctx context.Context, request openapi.GetStaffClassThreadRequestObject) (openapi.GetStaffClassThreadResponseObject, error) {
	who, ok := httpapi.ActorFromContext(ctx)
	if h.app == nil || !ok {
		return nil, httpx.ErrNotImplemented
	}
	p := request.Params
	thread, err := h.app.Queries.StaffClassThread.Handle(ctx, query.StaffClassThread{
		ReaderID: who.ID, Scope: who.Scope, ClassID: request.ClassId.String(), Cursor: cursorOf(p.Before, p.After, p.Limit),
	})
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return openapi.GetStaffClassThread404JSONResponse{NotFoundJSONResponse: threadNotFound(ctx)}, nil
	case errors.Is(err, domain.ErrInvalid):
		return openapi.GetStaffClassThread400JSONResponse{BadRequestJSONResponse: badRequest(ctx)}, nil
	case err != nil:
		return nil, err
	}
	c := thread.Channel
	return openapi.GetStaffClassThread200JSONResponse{
		Class: openapi.StaffClassThreadClass{
			Id: httpapi.ParseUUID(c.ClassID), Name: c.Name, Archived: c.Archived, StudentCount: c.StudentCount,
		},
		Messages:   messagesOf(thread.Page),
		NextBefore: uuidOf(thread.Page.NextBefore),
		LatestId:   uuidOf(thread.Page.LatestID),
	}, nil
}

// PostClassAnnouncement implements POST /teacher/messages/classes/{classId}.
func (h Staff) PostClassAnnouncement(ctx context.Context, request openapi.PostClassAnnouncementRequestObject) (openapi.PostClassAnnouncementResponseObject, error) {
	who, ok := httpapi.ActorFromContext(ctx)
	if h.app == nil || !ok || request.Body == nil {
		return nil, httpx.ErrNotImplemented
	}
	in := *request.Body
	posted, err := h.app.Commands.PostAnnouncement.Handle(ctx, command.PostAnnouncement{
		Actor: who, MayWrite: mayWriteClasses(ctx), ClassID: request.ClassId.String(), ClientID: in.ClientId.String(),
		Body: in.Body, AttachmentID: stringOf(in.AttachmentId),
	})
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return openapi.PostClassAnnouncement404JSONResponse{NotFoundJSONResponse: threadNotFound(ctx)}, nil
	case errors.Is(err, domain.ErrAttachmentNotFound):
		return openapi.PostClassAnnouncement404JSONResponse{NotFoundJSONResponse: attachmentNotFound(ctx)}, nil
	case errors.Is(err, domain.ErrForbidden):
		return openapi.PostClassAnnouncement403JSONResponse{ForbiddenJSONResponse: forbidden(ctx)}, nil
	case errors.Is(err, domain.ErrClassArchived):
		return openapi.PostClassAnnouncement409JSONResponse(classArchived(ctx)), nil
	case errors.Is(err, domain.ErrEmpty):
		return openapi.PostClassAnnouncement422JSONResponse(emptyMessage(ctx)), nil
	case errors.Is(err, domain.ErrClientIDTaken):
		return openapi.PostClassAnnouncement422JSONResponse(clientIDTaken(ctx)), nil
	case errors.Is(err, domain.ErrInvalid):
		return openapi.PostClassAnnouncement400JSONResponse{BadRequestJSONResponse: badRequest(ctx)}, nil
	case err != nil:
		return nil, err
	}
	if !posted.Created {
		return openapi.PostClassAnnouncement200JSONResponse(messageOf(posted.Message)), nil
	}
	return openapi.PostClassAnnouncement201JSONResponse(messageOf(posted.Message)), nil
}

// MarkStaffClassThreadRead implements POST /teacher/messages/classes/{classId}/read.
func (h Staff) MarkStaffClassThreadRead(ctx context.Context, request openapi.MarkStaffClassThreadReadRequestObject) (openapi.MarkStaffClassThreadReadResponseObject, error) {
	if h.app == nil || request.Body == nil {
		return nil, httpx.ErrNotImplemented
	}
	err := h.markRead(ctx, domain.ThreadRef{Kind: domain.KindClass, ClassID: request.ClassId.String()}, request.Body.LastReadMessageId)
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return openapi.MarkStaffClassThreadRead404JSONResponse{NotFoundJSONResponse: threadNotFound(ctx)}, nil
	case errors.Is(err, domain.ErrNotInThread):
		return openapi.MarkStaffClassThreadRead422JSONResponse(notInThread(ctx)), nil
	case err != nil:
		return nil, err
	}
	return openapi.MarkStaffClassThreadRead204Response{}, nil
}
