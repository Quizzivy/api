package router

import (
	"context"

	"quizzivy/gen/openapi"
	"quizzivy/internal/platform/httpx"
)

// R7Pending answers 501 for the R7 messaging operations the contract declares
// and no task has built yet (httpx.ErrNotImplemented): the attachment upload
// and its two downloads (T-R7.11), which moves them into the messaging
// module's transport and deletes the file. The staff side (T-R7.10a) is the
// messaging module's Staff and the student side (T-R7.10b) its Student.
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
