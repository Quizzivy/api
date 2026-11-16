package router

import (
	"context"

	"quizzivy/gen/openapi"
	"quizzivy/internal/platform/httpx"
)

// R7Pending answers 501 for the R7 messaging operations the contract declares
// and no task has built yet (httpx.ErrNotImplemented): the staff side
// (T-R7.10a), the student side (T-R7.10b) and attachments (T-R7.11). Each of
// those tasks moves its operations into the messaging module's transport and
// deletes them here; the last one deletes the file.
type R7Pending struct{}

func (R7Pending) UploadMessageAttachment(context.Context, openapi.UploadMessageAttachmentRequestObject) (openapi.UploadMessageAttachmentResponseObject, error) {
	return nil, httpx.ErrNotImplemented
}

func (R7Pending) GetStaffMessageAttachmentUrl(context.Context, openapi.GetStaffMessageAttachmentUrlRequestObject) (openapi.GetStaffMessageAttachmentUrlResponseObject, error) {
	return nil, httpx.ErrNotImplemented
}

func (R7Pending) GetStaffClassThread(context.Context, openapi.GetStaffClassThreadRequestObject) (openapi.GetStaffClassThreadResponseObject, error) {
	return nil, httpx.ErrNotImplemented
}

func (R7Pending) PostClassAnnouncement(context.Context, openapi.PostClassAnnouncementRequestObject) (openapi.PostClassAnnouncementResponseObject, error) {
	return nil, httpx.ErrNotImplemented
}

func (R7Pending) MarkStaffClassThreadRead(context.Context, openapi.MarkStaffClassThreadReadRequestObject) (openapi.MarkStaffClassThreadReadResponseObject, error) {
	return nil, httpx.ErrNotImplemented
}

func (R7Pending) GetStaffDirectThread(context.Context, openapi.GetStaffDirectThreadRequestObject) (openapi.GetStaffDirectThreadResponseObject, error) {
	return nil, httpx.ErrNotImplemented
}

func (R7Pending) PostStaffDirectMessage(context.Context, openapi.PostStaffDirectMessageRequestObject) (openapi.PostStaffDirectMessageResponseObject, error) {
	return nil, httpx.ErrNotImplemented
}

func (R7Pending) MarkStaffDirectThreadRead(context.Context, openapi.MarkStaffDirectThreadReadRequestObject) (openapi.MarkStaffDirectThreadReadResponseObject, error) {
	return nil, httpx.ErrNotImplemented
}

func (R7Pending) ListStaffThreads(context.Context, openapi.ListStaffThreadsRequestObject) (openapi.ListStaffThreadsResponseObject, error) {
	return nil, httpx.ErrNotImplemented
}

func (R7Pending) GetStudentMessageAttachmentUrl(context.Context, openapi.GetStudentMessageAttachmentUrlRequestObject) (openapi.GetStudentMessageAttachmentUrlResponseObject, error) {
	return nil, httpx.ErrNotImplemented
}

func (R7Pending) GetStudentClassThread(context.Context, openapi.GetStudentClassThreadRequestObject) (openapi.GetStudentClassThreadResponseObject, error) {
	return nil, httpx.ErrNotImplemented
}

func (R7Pending) MarkStudentClassThreadRead(context.Context, openapi.MarkStudentClassThreadReadRequestObject) (openapi.MarkStudentClassThreadReadResponseObject, error) {
	return nil, httpx.ErrNotImplemented
}

func (R7Pending) GetStudentDirectThread(context.Context, openapi.GetStudentDirectThreadRequestObject) (openapi.GetStudentDirectThreadResponseObject, error) {
	return nil, httpx.ErrNotImplemented
}

func (R7Pending) PostStudentDirectMessage(context.Context, openapi.PostStudentDirectMessageRequestObject) (openapi.PostStudentDirectMessageResponseObject, error) {
	return nil, httpx.ErrNotImplemented
}

func (R7Pending) MarkStudentDirectThreadRead(context.Context, openapi.MarkStudentDirectThreadReadRequestObject) (openapi.MarkStudentDirectThreadReadResponseObject, error) {
	return nil, httpx.ErrNotImplemented
}

func (R7Pending) ListStudentThreads(context.Context, openapi.ListStudentThreadsRequestObject) (openapi.ListStudentThreadsResponseObject, error) {
	return nil, httpx.ErrNotImplemented
}
