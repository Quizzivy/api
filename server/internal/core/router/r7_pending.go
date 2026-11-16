package router

import (
	"context"

	"quizzivy/gen/openapi"
	"quizzivy/internal/platform/httpx"
)

// R7Pending answers 501 for the R7 operations the contract declares and no
// task has built yet (httpx.ErrNotImplemented): editing a question in a test's
// context (T-R7.6) and the copies (T-R7.7). Each of those tasks moves its
// operations into its module's transport and deletes them here.
type R7Pending struct{}

func (R7Pending) EditTestQuestion(context.Context, openapi.EditTestQuestionRequestObject) (openapi.EditTestQuestionResponseObject, error) {
	return nil, httpx.ErrNotImplemented
}

func (R7Pending) CopyTest(context.Context, openapi.CopyTestRequestObject) (openapi.CopyTestResponseObject, error) {
	return nil, httpx.ErrNotImplemented
}

func (R7Pending) CopyQuestionsForTest(context.Context, openapi.CopyQuestionsForTestRequestObject) (openapi.CopyQuestionsForTestResponseObject, error) {
	return nil, httpx.ErrNotImplemented
}

func (R7Pending) CopyQuestionSet(context.Context, openapi.CopyQuestionSetRequestObject) (openapi.CopyQuestionSetResponseObject, error) {
	return nil, httpx.ErrNotImplemented
}
