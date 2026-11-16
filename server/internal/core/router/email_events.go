package router

import (
	"context"
	"errors"

	"quizzivy/gen/openapi"
	"quizzivy/internal/platform/email"
	"quizzivy/internal/platform/httpx"
)

// RawBodyOperations are the operations whose handler reads the request body
// byte for byte, for a signature over it (httpx.KeepRawBody).
func RawBodyOperations() []string {
	return []string{"POST /public/email-events"}
}

// EmailEvents is Resend's webhook, receiveEmailEvents (T-R7.15): the email
// platform's Webhook over the body KeepRawBody kept. Without
// RESEND_WEBHOOK_SECRET Webhook is nil and the operation answers 501, as an
// optional dependency does. Past request validation the answer is 204 or 401,
// both empty: nothing tells a caller whether an address is known or listed.
type EmailEvents struct {
	Webhook *email.Webhook
}

func (e EmailEvents) ReceiveEmailEvents(ctx context.Context, req openapi.ReceiveEmailEventsRequestObject) (openapi.ReceiveEmailEventsResponseObject, error) {
	if e.Webhook == nil {
		return nil, httpx.ErrNotImplemented
	}
	body, ok := httpx.RawBodyFromContext(ctx)
	if !ok {
		return openapi.ReceiveEmailEvents401Response{}, nil
	}
	err := e.Webhook.Receive(ctx, email.Delivery{
		ID: deref(req.Params.SvixId), Timestamp: deref(req.Params.SvixTimestamp), Signature: deref(req.Params.SvixSignature), Body: body,
	})
	switch {
	case errors.Is(err, email.ErrUnsigned):
		return openapi.ReceiveEmailEvents401Response{}, nil
	case err != nil:
		return nil, err
	}
	return openapi.ReceiveEmailEvents204Response{}, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
