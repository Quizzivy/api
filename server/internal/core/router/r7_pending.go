package router

import (
	"context"

	"quizzivy/gen/openapi"
	"quizzivy/internal/platform/httpx"
)

// R7Pending answers 501 for the R7 messaging operations the contract declares
// and no task has built yet (httpx.ErrNotImplemented): the student side
// (T-R7.10b) and attachments (T-R7.11). Each of those tasks moves its
// operations into the messaging module's transport and deletes them here; the
// last one deletes the file. The staff side (T-R7.10a) is the messaging
// module's Staff.
type R7Pending struct{}

func (R7Pending) UploadMessageAttachment(context.Context, openapi.UploadMessageAttachmentRequestObject) (openapi.UploadMessageAttachmentResponseObject, error) {
	return nil, httpx.ErrNotImplemented
}

func (R7Pending) GetStaffMessageAttachmentUrl(context.Context, openapi.GetStaffMessageAttachmentUrlRequestObject) (openapi.GetStaffMessageAttachmentUrlResponseObject, error) {
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
