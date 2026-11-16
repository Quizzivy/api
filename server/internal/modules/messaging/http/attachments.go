package http

import (
	"context"
	"errors"
	"mime/multipart"
	"net/http"

	"quizzivy/gen/openapi"
	"quizzivy/internal/modules/messaging/application/command"
	"quizzivy/internal/modules/messaging/application/query"
	"quizzivy/internal/modules/messaging/domain"
	"quizzivy/internal/platform/httpapi"
	"quizzivy/internal/platform/httpx"
)

// noStore keeps a download URL out of every cache: it is a credential, and
// it lives five minutes.
const noStore = "private, no-store"

func fileTooLarge(ctx context.Context) openapi.UploadMessageAttachment413JSONResponse {
	return openapi.UploadMessageAttachment413JSONResponse(httpapi.Error(ctx, openapi.MEDIATOOLARGE,
		httpx.Text(ctx, "Tệp vượt quá 20 MB. Vui lòng chọn tệp nhỏ hơn.", "The file is larger than 20 MB. Please choose a smaller one.")))
}

func noFile(ctx context.Context) openapi.UploadMessageAttachment400JSONResponse {
	return openapi.UploadMessageAttachment400JSONResponse{BadRequestJSONResponse: openapi.BadRequestJSONResponse(httpapi.FieldError(ctx, "file",
		httpx.Text(ctx, "Không tìm thấy tệp trong yêu cầu tải lên.", "The upload request holds no file.")))}
}

func unsupportedFile(ctx context.Context, code openapi.ErrorCode, message string) openapi.UploadMessageAttachment415JSONResponse {
	return openapi.UploadMessageAttachment415JSONResponse(httpapi.Error(ctx, code, message))
}

func overBodyLimit(err error) bool {
	var tooLarge *http.MaxBytesError
	return errors.As(err, &tooLarge)
}

func firstFilePart(reader *multipart.Reader) (*multipart.Part, error) {
	for {
		part, err := reader.NextPart()
		if err != nil {
			return nil, err
		}
		if part.FileName() != "" {
			return part, nil
		}
		_ = part.Close()
	}
}

// UploadMessageAttachment implements POST /teacher/messages/attachments.
func (h Staff) UploadMessageAttachment(ctx context.Context, request openapi.UploadMessageAttachmentRequestObject) (openapi.UploadMessageAttachmentResponseObject, error) {
	who, ok := httpapi.ActorFromContext(ctx)
	if h.app == nil || !ok || request.Body == nil {
		return nil, httpx.ErrNotImplemented
	}
	part, err := firstFilePart(request.Body)
	switch {
	case overBodyLimit(err):
		return fileTooLarge(ctx), nil
	case err != nil:
		return noFile(ctx), nil
	}
	defer func() { _ = part.Close() }()
	file, err := h.app.Commands.UploadAttachment.Handle(ctx, command.UploadAttachment{UploaderID: who.ID, Filename: part.FileName(), Body: part})
	switch {
	case errors.Is(err, domain.ErrStorageOff):
		return nil, httpx.ErrNotImplemented
	case errors.Is(err, domain.ErrTooLarge), overBodyLimit(err):
		return fileTooLarge(ctx), nil
	case errors.Is(err, domain.ErrInvalidFilename):
		return openapi.UploadMessageAttachment400JSONResponse{BadRequestJSONResponse: openapi.BadRequestJSONResponse(httpapi.FieldError(ctx, "filename",
			httpx.Text(ctx, "Tên tệp chứa ký tự không hợp lệ.", "The file name holds a character that is not allowed.")))}, nil
	case errors.Is(err, domain.ErrImageDimensions):
		return unsupportedFile(ctx, openapi.IMAGEDIMENSIONS, httpx.Text(ctx,
			"Ảnh lớn hơn 10000 × 10000 điểm ảnh. Vui lòng thu nhỏ ảnh.", "The image is larger than 10000 × 10000 pixels. Please make it smaller.")), nil
	case errors.Is(err, domain.ErrHasMacros):
		return unsupportedFile(ctx, openapi.ATTACHMENTHASMACROS, httpx.Text(ctx,
			"Tệp Word có macro. Hãy lưu lại thành tệp .docx không có macro rồi đính kèm lại.",
			"The Word file has macros. Save it as a .docx without macros, then attach it again.")), nil
	case errors.Is(err, domain.ErrUnsupportedType):
		return unsupportedFile(ctx, openapi.MEDIATYPEUNSUPPORTED, httpx.Text(ctx,
			"Chỉ đính kèm được PDF, Word (DOCX hoặc DOC), ảnh PNG hoặc JPEG, và âm thanh MP3 hoặc M4A.",
			"Only PDF, Word (DOCX or DOC), PNG or JPEG images, and MP3 or M4A audio can be attached.")), nil
	case err != nil:
		return nil, err
	}
	return openapi.UploadMessageAttachment201JSONResponse(attachmentOf(file)), nil
}

func attachmentOf(a domain.Attachment) openapi.MessageAttachment {
	return openapi.MessageAttachment{
		Id: httpapi.ParseUUID(a.ID), Filename: a.Filename, Bytes: a.Bytes, MimeType: openapi.MessageAttachmentMimeType(a.MimeType),
	}
}

func urlOf(u domain.AttachmentURL) openapi.AttachmentUrl {
	return openapi.AttachmentUrl{Url: u.URL, ExpiresAt: u.ExpiresAt.UTC()}
}

// GetStaffMessageAttachmentUrl implements GET /teacher/messages/attachments/{id}.
func (h Staff) GetStaffMessageAttachmentUrl(ctx context.Context, request openapi.GetStaffMessageAttachmentUrlRequestObject) (openapi.GetStaffMessageAttachmentUrlResponseObject, error) {
	who, ok := httpapi.ActorFromContext(ctx)
	if h.app == nil || !ok {
		return nil, httpx.ErrNotImplemented
	}
	signed, err := h.app.Queries.StaffAttachmentURL.Handle(ctx, query.StaffAttachmentURL{ReaderID: who.ID, Scope: who.Scope, ID: request.Id.String()})
	switch {
	case errors.Is(err, domain.ErrStorageOff):
		return nil, httpx.ErrNotImplemented
	case errors.Is(err, domain.ErrAttachmentNotFound):
		return openapi.GetStaffMessageAttachmentUrl404JSONResponse{NotFoundJSONResponse: fileNotFound(ctx)}, nil
	case err != nil:
		return nil, err
	}
	return openapi.GetStaffMessageAttachmentUrl200JSONResponse{
		Body: urlOf(signed), Headers: openapi.GetStaffMessageAttachmentUrl200ResponseHeaders{CacheControl: noStore},
	}, nil
}

// GetStudentMessageAttachmentUrl implements GET /app/messages/attachments/{id}.
func (h Student) GetStudentMessageAttachmentUrl(ctx context.Context, request openapi.GetStudentMessageAttachmentUrlRequestObject) (openapi.GetStudentMessageAttachmentUrlResponseObject, error) {
	who, ok := httpapi.ActorFromContext(ctx)
	if h.app == nil || !ok {
		return nil, httpx.ErrNotImplemented
	}
	signed, err := h.app.Queries.StudentAttachmentURL.Handle(ctx, query.StudentAttachmentURL{StudentID: who.ID, ID: request.Id.String()})
	switch {
	case errors.Is(err, domain.ErrStorageOff):
		return nil, httpx.ErrNotImplemented
	case errors.Is(err, domain.ErrAttachmentNotFound):
		return openapi.GetStudentMessageAttachmentUrl404JSONResponse{NotFoundJSONResponse: fileNotFound(ctx)}, nil
	case err != nil:
		return nil, err
	}
	return openapi.GetStudentMessageAttachmentUrl200JSONResponse{
		Body: urlOf(signed), Headers: openapi.GetStudentMessageAttachmentUrl200ResponseHeaders{CacheControl: noStore},
	}, nil
}

func fileNotFound(ctx context.Context) openapi.NotFoundJSONResponse {
	return openapi.NotFoundJSONResponse(httpapi.NotFound(ctx,
		httpx.Text(ctx, "Không tìm thấy tệp đính kèm này.", "This attachment was not found.")))
}
