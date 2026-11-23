package router

import (
	"context"

	"quizzivy/gen/openapi"
	"quizzivy/internal/platform/httpx"
)

// F2Pending answers 501 for the F2 messaging operations the contract declares
// and no task has built yet (httpx.ErrNotImplemented): the recipients finder
// (T-F2.3 for students, T-F2.4 for colleagues) and the colleague threads
// (T-F2.4). Each task moves its operations into the messaging module's
// transport and deletes them here; the last one deletes the file.
type F2Pending struct{}

// T-F2.3 and T-F2.4

func (F2Pending) ListMessageRecipients(context.Context, openapi.ListMessageRecipientsRequestObject) (openapi.ListMessageRecipientsResponseObject, error) {
	return nil, httpx.ErrNotImplemented
}

// T-F2.4

func (F2Pending) ListColleagueThreads(context.Context, openapi.ListColleagueThreadsRequestObject) (openapi.ListColleagueThreadsResponseObject, error) {
	return nil, httpx.ErrNotImplemented
}

func (F2Pending) GetColleagueThread(context.Context, openapi.GetColleagueThreadRequestObject) (openapi.GetColleagueThreadResponseObject, error) {
	return nil, httpx.ErrNotImplemented
}

func (F2Pending) PostColleagueMessage(context.Context, openapi.PostColleagueMessageRequestObject) (openapi.PostColleagueMessageResponseObject, error) {
	return nil, httpx.ErrNotImplemented
}

func (F2Pending) MarkColleagueThreadRead(context.Context, openapi.MarkColleagueThreadReadRequestObject) (openapi.MarkColleagueThreadReadResponseObject, error) {
	return nil, httpx.ErrNotImplemented
}
