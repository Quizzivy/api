package router

import (
	"context"

	"quizzivy/gen/openapi"
	"quizzivy/internal/platform/httpx"
)

// R7Pending answers 501 for the R7 email operation the contract declares and
// no task has built yet (httpx.ErrNotImplemented): Resend's webhook,
// receiveEmailEvents (T-R7.15), which moves it into the email platform's
// transport and deletes this file.
type R7Pending struct{}

func (R7Pending) ReceiveEmailEvents(context.Context, openapi.ReceiveEmailEventsRequestObject) (openapi.ReceiveEmailEventsResponseObject, error) {
	return nil, httpx.ErrNotImplemented
}
