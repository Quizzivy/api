package router

import (
	"context"

	"quizzivy/gen/openapi"
	"quizzivy/internal/platform/httpx"
)

// R7Pending answers 501 for the R7 sharing operations the contract declares and
// no task has built yet (httpx.ErrNotImplemented): sharing and Shared with me
// (T-R7.3), editing a question in a test's context (T-R7.6), and the copies
// (T-R7.7). Each of those tasks moves its operations into its module's
// transport and deletes them here.
type R7Pending struct{}

func (R7Pending) SearchTeacherDirectory(context.Context, openapi.SearchTeacherDirectoryRequestObject) (openapi.SearchTeacherDirectoryResponseObject, error) {
	return nil, httpx.ErrNotImplemented
}

func (R7Pending) ListShares(context.Context, openapi.ListSharesRequestObject) (openapi.ListSharesResponseObject, error) {
	return nil, httpx.ErrNotImplemented
}

func (R7Pending) CreateShares(context.Context, openapi.CreateSharesRequestObject) (openapi.CreateSharesResponseObject, error) {
	return nil, httpx.ErrNotImplemented
}

func (R7Pending) UpdateShare(context.Context, openapi.UpdateShareRequestObject) (openapi.UpdateShareResponseObject, error) {
	return nil, httpx.ErrNotImplemented
}

func (R7Pending) DeleteShare(context.Context, openapi.DeleteShareRequestObject) (openapi.DeleteShareResponseObject, error) {
	return nil, httpx.ErrNotImplemented
}

func (R7Pending) ListSharedWithMe(context.Context, openapi.ListSharedWithMeRequestObject) (openapi.ListSharedWithMeResponseObject, error) {
	return nil, httpx.ErrNotImplemented
}

func (R7Pending) RemoveSharedWithMe(context.Context, openapi.RemoveSharedWithMeRequestObject) (openapi.RemoveSharedWithMeResponseObject, error) {
	return nil, httpx.ErrNotImplemented
}

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
