package http

import (
	"context"
	"errors"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"quizzivy/gen/openapi"
	"quizzivy/internal/modules/organization/application/command"
	"quizzivy/internal/modules/organization/application/query"
	"quizzivy/internal/modules/organization/domain"
	"quizzivy/internal/platform/httpapi"
	"quizzivy/internal/platform/httpx"
	"quizzivy/internal/shared/validation"
)

// ListTerms implements GET /admin/settings/terms.
func (h Organization) ListTerms(ctx context.Context, _ openapi.ListTermsRequestObject) (openapi.ListTermsResponseObject, error) {
	who, ok := httpapi.ActorFromContext(ctx)
	if h.app == nil || !ok {
		return nil, httpx.ErrNotImplemented
	}
	listed, err := h.app.Queries.ListTerms.Handle(ctx, query.ListTerms{ViewerID: who.ID})
	if err != nil {
		return nil, err
	}
	items := make([]openapi.Term, len(listed))
	for i, t := range listed {
		items[i] = termBody(t)
	}
	return openapi.ListTerms200JSONResponse{Items: items}, nil
}

// CreateTerm implements POST /admin/settings/terms.
func (h Organization) CreateTerm(ctx context.Context, request openapi.CreateTermRequestObject) (openapi.CreateTermResponseObject, error) {
	who, ok := httpapi.ActorFromContext(ctx)
	if h.app == nil || !ok || request.Body == nil {
		return nil, httpx.ErrNotImplemented
	}
	b := request.Body
	created, err := h.app.Commands.CreateTerm.Handle(ctx, command.CreateTerm{
		Actor: who, Draft: domain.TermDraft{Name: b.Name, StartsOn: b.StartsOn.Time, EndsOn: b.EndsOn.Time},
	})
	var invalid *validation.Error
	var overlap *domain.TermOverlap
	switch {
	case err == nil:
		return openapi.CreateTerm201JSONResponse(termBody(created)), nil
	case errors.As(err, &invalid):
		return openapi.CreateTerm400JSONResponse(invalidTerm(ctx, invalid)), nil
	case errors.As(err, &overlap):
		return openapi.CreateTerm409JSONResponse(overlapping(ctx, overlap)), nil
	default:
		return nil, err
	}
}

// UpdateTerm implements PATCH /admin/settings/terms/{id}.
func (h Organization) UpdateTerm(ctx context.Context, request openapi.UpdateTermRequestObject) (openapi.UpdateTermResponseObject, error) {
	who, ok := httpapi.ActorFromContext(ctx)
	if h.app == nil || !ok || request.Body == nil {
		return nil, httpx.ErrNotImplemented
	}
	b := request.Body
	saved, err := h.app.Commands.UpdateTerm.Handle(ctx, command.UpdateTerm{
		Actor: who, ID: request.Id.String(),
		Patch: domain.TermPatch{Name: b.Name, StartsOn: dayOf(b.StartsOn), EndsOn: dayOf(b.EndsOn)},
	})
	var invalid *validation.Error
	var overlap *domain.TermOverlap
	switch {
	case err == nil:
		return openapi.UpdateTerm200JSONResponse(termBody(saved)), nil
	case errors.As(err, &invalid):
		return openapi.UpdateTerm400JSONResponse(invalidTerm(ctx, invalid)), nil
	case errors.Is(err, domain.ErrTermNotFound):
		return openapi.UpdateTerm404JSONResponse{NotFoundJSONResponse: openapi.NotFoundJSONResponse(termNotFound(ctx))}, nil
	case errors.As(err, &overlap):
		return openapi.UpdateTerm409JSONResponse(overlapping(ctx, overlap)), nil
	default:
		return nil, err
	}
}

// DeleteTerm implements DELETE /admin/settings/terms/{id}.
func (h Organization) DeleteTerm(ctx context.Context, request openapi.DeleteTermRequestObject) (openapi.DeleteTermResponseObject, error) {
	who, ok := httpapi.ActorFromContext(ctx)
	if h.app == nil || !ok {
		return nil, httpx.ErrNotImplemented
	}
	_, err := h.app.Commands.DeleteTerm.Handle(ctx, command.DeleteTerm{Actor: who, ID: request.Id.String()})
	switch {
	case err == nil:
		return openapi.DeleteTerm204Response{}, nil
	case errors.Is(err, domain.ErrTermNotFound):
		return openapi.DeleteTerm404JSONResponse{NotFoundJSONResponse: openapi.NotFoundJSONResponse(termNotFound(ctx))}, nil
	default:
		return nil, err
	}
}

func termBody(t domain.StatedTerm) openapi.Term {
	return openapi.Term{
		Id: httpapi.ParseUUID(t.ID), Name: t.Name, State: openapi.TermState(t.State),
		StartsOn: openapi_types.Date{Time: t.StartsOn}, EndsOn: openapi_types.Date{Time: t.EndsOn},
	}
}

func dayOf(d *openapi_types.Date) *time.Time {
	if d == nil {
		return nil
	}
	return &d.Time
}

func invalidTerm(ctx context.Context, invalid *validation.Error) openapi.ErrorResponse {
	return httpapi.Invalid(ctx, httpx.Text(ctx, "Học kỳ không hợp lệ.", "The term is not valid."), invalid)
}

func overlapping(ctx context.Context, overlap *domain.TermOverlap) openapi.ErrorResponse {
	other := overlap.Other
	return httpapi.ErrorWithDetails(ctx, openapi.TERMOVERLAPS,
		httpx.Text(ctx, "Học kỳ này trùng ngày với một học kỳ khác.", "This term shares days with another term."),
		map[string]interface{}{"termId": other.ID, "name": other.Name})
}

func termNotFound(ctx context.Context) openapi.ErrorResponse {
	return httpapi.NotFound(ctx, httpx.Text(ctx, "Không tìm thấy học kỳ.", "The term was not found."))
}
