package http

import (
	"context"
	"errors"

	"quizzivy/gen/openapi"
	"quizzivy/internal/modules/leads/application/command"
	"quizzivy/internal/modules/leads/domain"
	"quizzivy/internal/platform/httpapi"
	"quizzivy/internal/platform/httpx"
	"quizzivy/internal/shared/validation"
)

// SubmitLead implements POST /public/leads. Every request that passes
// validation gets the one answer, stored or not.
func (h Leads) SubmitLead(ctx context.Context, request openapi.SubmitLeadRequestObject) (openapi.SubmitLeadResponseObject, error) {
	if h.app == nil || request.Body == nil {
		return nil, httpx.ErrNotImplemented
	}
	_, err := h.app.Commands.Submit.Handle(ctx, command.Submit{Form: formOf(*request.Body)})
	var invalid *validation.Error
	switch {
	case errors.As(err, &invalid):
		return openapi.SubmitLead400JSONResponse(httpapi.Invalid(ctx,
			httpx.Text(ctx, "Thông tin gửi đi chưa hợp lệ.", "The request is not valid."), invalid)), nil
	case err != nil:
		return nil, err
	}
	return openapi.SubmitLead202JSONResponse{Received: openapi.LeadReceiptReceivedTrue}, nil
}

func formOf(in openapi.LeadInput) domain.Form {
	interests := make([]string, len(in.Interests))
	for i, interest := range in.Interests {
		interests[i] = string(interest)
	}
	return domain.Form{
		FullName: in.FullName, Phone: in.Phone, Interests: interests, CourseFor: string(in.CourseFor),
		CallTime: string(in.CallTime), Locale: string(in.Locale), PrivacyNoticeVersion: string(in.PrivacyNoticeVersion),
		Website: httpapi.DerefString(in.Website),
	}
}
