package http

import (
	"context"
	"errors"

	"quizzivy/gen/openapi"
	mediadomain "quizzivy/internal/modules/media/domain"
	questions "quizzivy/internal/modules/questions/domain"
	questionshttp "quizzivy/internal/modules/questions/http"
	"quizzivy/internal/modules/tests/application/command"
	"quizzivy/internal/modules/tests/domain"
	"quizzivy/internal/platform/httpapi"
	"quizzivy/internal/platform/httpx"
)

// CopyTest is "Make a copy" (D4, T-R7.7): the current draft of a test the
// caller can read becomes a test of the caller's own, with copies of its
// questions. It answers the copy, as duplicateTest answers its own.
func (h Tests) CopyTest(ctx context.Context, request openapi.CopyTestRequestObject) (openapi.CopyTestResponseObject, error) {
	if h.app == nil {
		return nil, httpx.ErrNotImplemented
	}
	req, ok := testRequest(ctx, request.Id.String())
	if !ok {
		return nil, httpx.ErrNotImplemented
	}
	t, err := h.app.Commands.Copy.Handle(ctx, command.Copy{Request: req})
	switch {
	case errors.Is(err, domain.ErrNotFound), errors.Is(err, domain.ErrUnknownQuestion):
		return openapi.CopyTest404JSONResponse{NotFoundJSONResponse: openapi.NotFoundJSONResponse(
			httpapi.NotFound(ctx, msgTestNotFound(ctx)))}, nil
	case copyMediaGone(err):
		return openapi.CopyTest400JSONResponse{BadRequestJSONResponse: openapi.BadRequestJSONResponse(questionshttp.CopyMediaGone(ctx))}, nil
	case err != nil:
		return nil, err
	}
	out, err := toAPITest(t)
	if err != nil {
		return nil, err
	}
	return openapi.CopyTest201JSONResponse(out), nil
}

// CopyQuestionsForTest is how the builder adds bank questions for a caller
// who is not the test's owner (T-R7.7): copies made in the owner's bank, with
// the caller as their creator, which the builder's next autosave names.
func (h Tests) CopyQuestionsForTest(ctx context.Context, request openapi.CopyQuestionsForTestRequestObject) (openapi.CopyQuestionsForTestResponseObject, error) {
	if h.app == nil || request.Body == nil {
		return nil, httpx.ErrNotImplemented
	}
	req, ok := testRequest(ctx, request.Id.String())
	if !ok {
		return nil, httpx.ErrNotImplemented
	}
	ids := make([]string, len(request.Body.QuestionIds))
	for i, id := range request.Body.QuestionIds {
		ids[i] = id.String()
	}
	copies, err := h.app.Commands.CopyQuestions.Handle(ctx, command.CopyQuestions{Copies: domain.QuestionCopies{Request: req, QuestionIDs: ids}})
	if refused, ok := shareRefusal(ctx, err); ok {
		return openapi.CopyQuestionsForTest403JSONResponse(refused.body), nil
	}
	var missing *questions.QuestionsNotFoundError
	switch {
	case errors.As(err, &missing):
		return openapi.CopyQuestionsForTest404JSONResponse(questionshttp.QuestionsNotFound(ctx, missing)), nil
	case errors.Is(err, domain.ErrNotFound):
		return openapi.CopyQuestionsForTest404JSONResponse(httpapi.NotFound(ctx, msgTestNotFound(ctx))), nil
	case copyMediaGone(err):
		return openapi.CopyQuestionsForTest400JSONResponse{BadRequestJSONResponse: openapi.BadRequestJSONResponse(questionshttp.CopyMediaGone(ctx))}, nil
	case err != nil:
		return nil, err
	}
	out := openapi.CopyQuestionsForTest201JSONResponse{QuestionIds: make([]openapi.Uuid, len(copies))}
	for i, id := range copies {
		out.QuestionIds[i] = httpapi.ParseUUID(id)
	}
	return out, nil
}

// copyMediaGone reports whether a copy failed on a file a source binds that
// was deleted after it bound it: through a question, or through a group.
func copyMediaGone(err error) bool {
	return errors.Is(err, questions.ErrMediaNotFound) || errors.Is(err, mediadomain.ErrNotFound)
}
