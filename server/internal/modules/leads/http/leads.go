// Package http is the consultation requests' transport: the landing site's open submission under /public/leads and the Admin console's list, read, change and removal under /admin/leads.
package http

import (
	"context"

	"quizzivy/gen/openapi"
	"quizzivy/internal/modules/leads/application"
	"quizzivy/internal/modules/leads/domain"
	"quizzivy/internal/platform/httpapi"
	"quizzivy/internal/platform/httpx"
)

const privateCacheControl = "private, no-store"

// Leads serves the consultation requests.
type Leads struct {
	app *application.Application
}

// NewLeads builds the transport. A nil application answers 501.
func NewLeads(app *application.Application) Leads { return Leads{app: app} }

func leadBody(l domain.Lead) openapi.Lead {
	interests := make([]openapi.LeadInterest, len(l.Interests))
	for i, interest := range l.Interests {
		interests[i] = openapi.LeadInterest(interest)
	}
	out := openapi.Lead{
		Id: httpapi.ParseUUID(l.ID), FullName: l.FullName, Phone: l.PhoneDigits, Interests: interests,
		CourseFor: openapi.LeadCourseFor(l.CourseFor), CallTime: openapi.LeadCallTime(l.CallTime),
		Locale: openapi.LeadLocale(l.Locale), PrivacyNoticeVersion: l.PrivacyNoticeVersion,
		ConsentedAt: l.ConsentedAt.UTC(), Status: openapi.LeadStatus(l.Status), CreatedAt: l.CreatedAt.UTC(),
	}
	if l.Note != "" {
		out.Note = &l.Note
	}
	if l.HandledAt != nil {
		at := l.HandledAt.UTC()
		out.HandledAt = &at
	}
	if h := l.HandledBy; h != nil {
		out.HandledBy = &struct {
			Id   openapi.Uuid `json:"id"`
			Name string       `json:"name"`
		}{Id: httpapi.ParseUUID(h.ID), Name: h.Name}
	}
	return out
}

func notFound(ctx context.Context) openapi.NotFoundJSONResponse {
	return openapi.NotFoundJSONResponse(httpapi.NotFound(ctx,
		httpx.Text(ctx, "Không tìm thấy yêu cầu tư vấn này.", "This consultation request was not found.")))
}
