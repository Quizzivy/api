package http

import (
	"context"
	"errors"

	"quizzivy/gen/openapi"
	questions "quizzivy/internal/modules/questions/domain"
	questionshttp "quizzivy/internal/modules/questions/http"
	"quizzivy/internal/modules/tests/application/command"
	"quizzivy/internal/modules/tests/domain"
	"quizzivy/internal/platform/httpapi"
	"quizzivy/internal/platform/httpx"
)

// EditTestQuestion is how the builder saves a standalone question of a test's
// draft in the test's context (copy-on-write, D4, T-R7.6). It answers the
// question as saved, the copy when the edit forked it, and the test's new
// revision.
func (h Tests) EditTestQuestion(ctx context.Context, request openapi.EditTestQuestionRequestObject) (openapi.EditTestQuestionResponseObject, error) {
	if h.app == nil || request.Body == nil {
		return nil, httpx.ErrNotImplemented
	}
	req, ok := testRequest(ctx, request.Id.String())
	if !ok {
		return nil, httpx.ErrNotImplemented
	}
	edited, err := h.app.Commands.EditQuestion.Handle(ctx, command.EditQuestion{Edit: domain.QuestionEdit{
		Request:           req,
		QuestionID:        request.QuestionId.String(),
		Input:             questionshttp.ToQuestionInput(*request.Body),
		ExpectedUpdatedAt: request.Body.ExpectedUpdatedAt,
	}})
	if err != nil {
		return editQuestionRefusal(ctx, err)
	}
	question, err := questionshttp.ToAPIQuestion(ctx, h.media, edited.Question)
	if err != nil {
		return nil, err
	}
	return openapi.EditTestQuestion200JSONResponse{Question: question, Forked: edited.Forked, TestUpdatedAt: edited.TestUpdatedAt}, nil
}

func editQuestionRefusal(ctx context.Context, err error) (openapi.EditTestQuestionResponseObject, error) {
	if refused, ok := shareRefusal(ctx, err); ok {
		return openapi.EditTestQuestion403JSONResponse(refused.body), nil
	}
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return openapi.EditTestQuestion404JSONResponse{NotFoundJSONResponse: openapi.NotFoundJSONResponse(
			httpapi.NotFound(ctx, msgTestNotFound(ctx)))}, nil
	case errors.Is(err, domain.ErrQuestionNotInDraft):
		return openapi.EditTestQuestion404JSONResponse{NotFoundJSONResponse: openapi.NotFoundJSONResponse(
			httpapi.NotFound(ctx, httpx.Text(ctx, "Không tìm thấy câu hỏi trong đề.", "The question was not found in the test.")))}, nil
	case errors.Is(err, domain.ErrArchived):
		return openapi.EditTestQuestion409JSONResponse(httpapi.Error(ctx, openapi.TESTARCHIVED,
			httpx.Text(ctx, "Hãy khôi phục đề đã lưu trữ trước khi sửa câu hỏi.",
				"Restore the archived test before you edit its questions."))), nil
	case errors.Is(err, questions.ErrStaleWrite):
		return openapi.EditTestQuestion409JSONResponse(httpapi.StaleWrite(ctx,
			httpx.Text(ctx, "Câu hỏi đã được sửa ở nơi khác. Vui lòng tải lại trước khi lưu.",
				"The question was edited elsewhere. Please reload before saving."), err)), nil
	}
	if resp, handled := questionshttp.QuestionWriteError(ctx, err); handled {
		return openapi.EditTestQuestion400JSONResponse{BadRequestJSONResponse: openapi.BadRequestJSONResponse(resp)}, nil
	}
	return nil, err
}
