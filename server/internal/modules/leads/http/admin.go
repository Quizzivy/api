package http

import (
	"context"
	"encoding/json"
	"errors"

	"quizzivy/gen/openapi"
	"quizzivy/internal/modules/leads/application/command"
	"quizzivy/internal/modules/leads/application/query"
	"quizzivy/internal/modules/leads/domain"
	"quizzivy/internal/platform/httpapi"
	"quizzivy/internal/platform/httpx"
	"quizzivy/internal/shared/validation"
)

// ListLeads implements GET /admin/leads.
func (h Leads) ListLeads(ctx context.Context, request openapi.ListLeadsRequestObject) (openapi.ListLeadsResponseObject, error) {
	if h.app == nil {
		return nil, httpx.ErrNotImplemented
	}
	q := query.List{Q: httpapi.DerefString(request.Params.Q)}
	if request.Params.Status != nil {
		q.Status = domain.Status(*request.Params.Status)
	}
	if request.Params.Page != nil {
		q.Page = *request.Params.Page
	}
	if request.Params.Limit != nil {
		q.Limit = int(*request.Params.Limit)
	}
	listed, err := h.app.Queries.List.Handle(ctx, q)
	if err != nil {
		return nil, err
	}
	out := openapi.ListLeads200JSONResponse{Headers: openapi.ListLeads200ResponseHeaders{CacheControl: privateCacheControl}}
	out.Body.Items = make([]openapi.Lead, len(listed.Leads))
	for i, lead := range listed.Leads {
		out.Body.Items[i] = leadBody(lead)
	}
	out.Body.Page, out.Body.PageSize, out.Body.Total = listed.Page.Number, listed.Page.Size, listed.Page.Total
	return out, nil
}

// GetLead implements GET /admin/leads/{id}.
func (h Leads) GetLead(ctx context.Context, request openapi.GetLeadRequestObject) (openapi.GetLeadResponseObject, error) {
	if h.app == nil {
		return nil, httpx.ErrNotImplemented
	}
	lead, err := h.app.Queries.Get.Handle(ctx, query.Get{ID: request.Id.String()})
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return openapi.GetLead404JSONResponse{NotFoundJSONResponse: notFound(ctx)}, nil
	case err != nil:
		return nil, err
	}
	return openapi.GetLead200JSONResponse{
		Body:    leadBody(lead),
		Headers: openapi.GetLead200ResponseHeaders{CacheControl: privateCacheControl},
	}, nil
}

// UpdateLead implements PATCH /admin/leads/{id}. A body without a note keeps
// the stored one, a null or a blank one removes it, and any other is stored
// trimmed.
func (h Leads) UpdateLead(ctx context.Context, request openapi.UpdateLeadRequestObject) (openapi.UpdateLeadResponseObject, error) {
	who, ok := httpapi.ActorFromContext(ctx)
	if h.app == nil || !ok || request.Body == nil {
		return nil, httpx.ErrNotImplemented
	}
	var lead domain.Lead
	change, err := changeOf(*request.Body)
	if err == nil {
		lead, err = h.app.Commands.Update.Handle(ctx, command.Update{Actor: who, ID: request.Id.String(), Change: change})
	}
	var invalid *validation.Error
	switch {
	case errors.As(err, &invalid):
		return openapi.UpdateLead400JSONResponse{BadRequestJSONResponse: openapi.BadRequestJSONResponse(httpapi.Invalid(ctx,
			httpx.Text(ctx, "Ghi chú không hợp lệ.", "The note is not valid."), invalid))}, nil
	case errors.Is(err, domain.ErrNotFound):
		return openapi.UpdateLead404JSONResponse{NotFoundJSONResponse: notFound(ctx)}, nil
	case err != nil:
		return nil, err
	}
	return openapi.UpdateLead200JSONResponse(leadBody(lead)), nil
}

func changeOf(in openapi.LeadUpdate) (domain.Change, error) {
	var change domain.Change
	if in.Status != nil {
		status := domain.Status(*in.Status)
		change.Status = &status
	}
	if len(in.Note) > 0 {
		var note *string
		if err := json.Unmarshal(in.Note, &note); err != nil {
			return domain.Change{}, validation.Refuse(domain.FieldNote, "Ghi chú phải là văn bản.", "The note must be text.")
		}
		change.NoteSet = true
		if note != nil {
			change.Note = *note
		}
	}
	return change, nil
}

// DeleteLead implements DELETE /admin/leads/{id}.
func (h Leads) DeleteLead(ctx context.Context, request openapi.DeleteLeadRequestObject) (openapi.DeleteLeadResponseObject, error) {
	who, ok := httpapi.ActorFromContext(ctx)
	if h.app == nil || !ok {
		return nil, httpx.ErrNotImplemented
	}
	_, err := h.app.Commands.Delete.Handle(ctx, command.Delete{Actor: who, ID: request.Id.String()})
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return openapi.DeleteLead404JSONResponse{NotFoundJSONResponse: notFound(ctx)}, nil
	case err != nil:
		return nil, err
	}
	return openapi.DeleteLead204Response{}, nil
}
