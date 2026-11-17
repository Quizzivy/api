// Package http is the sharing context's transport: the directory of colleagues
// under /teacher/directory, grants under /teacher/shares and Shared with me
// under /teacher/shared-with-me.
package http

import (
	"context"
	"errors"

	"quizzivy/gen/openapi"
	"quizzivy/internal/modules/sharing/application"
	"quizzivy/internal/modules/sharing/application/command"
	"quizzivy/internal/modules/sharing/application/query"
	"quizzivy/internal/modules/sharing/domain"
	"quizzivy/internal/platform/httpapi"
	"quizzivy/internal/platform/httpx"
)

// Sharing serves the sharing operations.
type Sharing struct {
	app *application.Application
}

// NewSharing builds the transport. A nil application answers 501.
func NewSharing(app *application.Application) Sharing { return Sharing{app: app} }

func notFound(ctx context.Context) openapi.NotFoundJSONResponse {
	return openapi.NotFoundJSONResponse(httpapi.NotFound(ctx,
		httpx.Text(ctx, "Không tìm thấy nội dung hoặc lượt chia sẻ này.", "This content or share was not found.")))
}

func ownerOnly(ctx context.Context) openapi.ErrorResponse {
	return httpapi.Error(ctx, openapi.SHAREOWNERONLY,
		httpx.Text(ctx, "Chỉ chủ sở hữu mới chia sẻ hoặc đổi quyền chia sẻ được.", "Only the owner can share this or change who it is shared with."))
}

func invalid(ctx context.Context) openapi.BadRequestJSONResponse {
	return openapi.BadRequestJSONResponse(httpapi.Error(ctx, openapi.VALIDATIONFAILED,
		httpx.Text(ctx, "Yêu cầu chia sẻ không hợp lệ.", "The share request is not valid.")))
}

func teacherOf(p domain.Person) openapi.ShareTeacher {
	return openapi.ShareTeacher{
		Id: httpapi.ParseUUID(p.ID), FullName: p.FullName, DisplayName: p.DisplayName, Email: p.Email,
		AvatarUrl: p.AvatarURL, Disabled: p.Disabled,
	}
}

func grantOf(g domain.Grant) openapi.ShareGrant {
	return openapi.ShareGrant{
		Id: httpapi.ParseUUID(g.ID), Grantee: teacherOf(g.Grantee), Access: openapi.ShareAccess(g.Access),
		GrantedAt: g.GrantedAt.UTC(), GrantedBy: openapi.PersonRef{Id: httpapi.ParseUUID(g.GrantedBy.ID), Name: g.GrantedBy.Name},
	}
}

// SearchTeacherDirectory implements GET /teacher/directory/teachers.
func (h Sharing) SearchTeacherDirectory(ctx context.Context, request openapi.SearchTeacherDirectoryRequestObject) (openapi.SearchTeacherDirectoryResponseObject, error) {
	if h.app == nil {
		return nil, httpx.ErrNotImplemented
	}
	q := query.Directory{ActorID: httpapi.ActorID(ctx), Query: httpapi.DerefString(request.Params.Q)}
	if request.Params.Limit != nil {
		q.Limit = *request.Params.Limit
	}
	people, err := h.app.Queries.Directory.Handle(ctx, q)
	if err != nil {
		return nil, err
	}
	out := openapi.SearchTeacherDirectory200JSONResponse{Teachers: make([]openapi.ShareTeacher, len(people))}
	for i, p := range people {
		out.Teachers[i] = teacherOf(p)
	}
	return out, nil
}

// ListShares implements GET /teacher/shares.
func (h Sharing) ListShares(ctx context.Context, request openapi.ListSharesRequestObject) (openapi.ListSharesResponseObject, error) {
	if h.app == nil {
		return nil, httpx.ErrNotImplemented
	}
	listing, err := h.app.Queries.ListShares.Handle(ctx, query.ListShares{
		Scope: httpapi.ScopeFromContext(ctx), Kind: domain.Kind(request.Params.Kind), ResourceID: request.Params.ResourceId.String(),
	})
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return openapi.ListShares404JSONResponse{NotFoundJSONResponse: notFound(ctx)}, nil
	case errors.Is(err, domain.ErrOwnerOnly):
		return openapi.ListShares403JSONResponse(ownerOnly(ctx)), nil
	case errors.Is(err, domain.ErrInvalid):
		return openapi.ListShares400JSONResponse{BadRequestJSONResponse: invalid(ctx)}, nil
	case err != nil:
		return nil, err
	}
	out := openapi.ListShares200JSONResponse{
		Owner: openapi.ShareOwner{
			Id: httpapi.ParseUUID(listing.Owner.ID), Name: listing.Owner.DisplayName, Email: listing.Owner.Email, AvatarUrl: listing.Owner.AvatarURL,
		},
		Grants: make([]openapi.ShareGrant, len(listing.Grants)),
	}
	for i, g := range listing.Grants {
		out.Grants[i] = grantOf(g)
	}
	return out, nil
}

// CreateShares implements POST /teacher/shares.
func (h Sharing) CreateShares(ctx context.Context, request openapi.CreateSharesRequestObject) (openapi.CreateSharesResponseObject, error) {
	who, ok := httpapi.ActorFromContext(ctx)
	if h.app == nil || !ok || request.Body == nil {
		return nil, httpx.ErrNotImplemented
	}
	in := *request.Body
	grantees := make([]string, len(in.GranteeIds))
	for i, id := range in.GranteeIds {
		grantees[i] = id.String()
	}
	granted, err := h.app.Commands.CreateShares.Handle(ctx, command.CreateShares{
		Actor: who, Kind: domain.Kind(in.Kind), ResourceID: in.ResourceId.String(), GranteeIDs: grantees,
		Access: domain.Access(in.Access), Notify: in.Notify, Message: in.Message,
	})
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return openapi.CreateShares404JSONResponse{NotFoundJSONResponse: notFound(ctx)}, nil
	case errors.Is(err, domain.ErrOwnerOnly):
		return openapi.CreateShares403JSONResponse(ownerOnly(ctx)), nil
	case errors.Is(err, domain.ErrInvalid):
		return openapi.CreateShares400JSONResponse{BadRequestJSONResponse: invalid(ctx)}, nil
	case err != nil:
		return nil, err
	}
	out := openapi.CreateShares201JSONResponse{Grants: make([]openapi.ShareGrant, len(granted.Grants))}
	for i, g := range granted.Grants {
		out.Grants[i] = grantOf(g)
	}
	out.Skipped = make([]struct {
		Code   openapi.ShareSkipCode `json:"code"`
		UserId openapi.Uuid          `json:"userId"`
	}, len(granted.Skipped))
	for i, s := range granted.Skipped {
		out.Skipped[i].Code, out.Skipped[i].UserId = openapi.ShareSkipCode(s.Code), httpapi.ParseUUID(s.UserID)
	}
	return out, nil
}

// UpdateShare implements PATCH /teacher/shares/{shareId}.
func (h Sharing) UpdateShare(ctx context.Context, request openapi.UpdateShareRequestObject) (openapi.UpdateShareResponseObject, error) {
	who, ok := httpapi.ActorFromContext(ctx)
	if h.app == nil || !ok || request.Body == nil {
		return nil, httpx.ErrNotImplemented
	}
	grant, err := h.app.Commands.UpdateShare.Handle(ctx, command.UpdateShare{
		Actor: who, ShareID: request.ShareId.String(), Access: domain.Access(request.Body.Access),
	})
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return openapi.UpdateShare404JSONResponse{NotFoundJSONResponse: notFound(ctx)}, nil
	case errors.Is(err, domain.ErrOwnerOnly):
		return openapi.UpdateShare403JSONResponse(ownerOnly(ctx)), nil
	case errors.Is(err, domain.ErrInvalid):
		return openapi.UpdateShare400JSONResponse{BadRequestJSONResponse: invalid(ctx)}, nil
	case err != nil:
		return nil, err
	}
	return openapi.UpdateShare200JSONResponse(grantOf(grant)), nil
}

// DeleteShare implements DELETE /teacher/shares/{shareId}.
func (h Sharing) DeleteShare(ctx context.Context, request openapi.DeleteShareRequestObject) (openapi.DeleteShareResponseObject, error) {
	who, ok := httpapi.ActorFromContext(ctx)
	if h.app == nil || !ok {
		return nil, httpx.ErrNotImplemented
	}
	_, err := h.app.Commands.DeleteShare.Handle(ctx, command.DeleteShare{Actor: who, ShareID: request.ShareId.String()})
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return openapi.DeleteShare404JSONResponse{NotFoundJSONResponse: notFound(ctx)}, nil
	case errors.Is(err, domain.ErrOwnerOnly):
		return openapi.DeleteShare403JSONResponse(ownerOnly(ctx)), nil
	case err != nil:
		return nil, err
	}
	return openapi.DeleteShare204Response{}, nil
}

// ListSharedWithMe implements GET /teacher/shared-with-me.
func (h Sharing) ListSharedWithMe(ctx context.Context, request openapi.ListSharedWithMeRequestObject) (openapi.ListSharedWithMeResponseObject, error) {
	if h.app == nil {
		return nil, httpx.ErrNotImplemented
	}
	p := request.Params
	filter := domain.SharedFilter{UserID: httpapi.ActorID(ctx), Query: httpapi.DerefString(p.Q)}
	if p.Kind != nil {
		filter.Kind = domain.Kind(*p.Kind)
	}
	if p.Page != nil {
		filter.Page = *p.Page
	}
	if p.Limit != nil {
		filter.Limit = *p.Limit
	}
	shared, err := h.app.Queries.SharedWithMe.Handle(ctx, query.SharedWithMe{Filter: filter})
	switch {
	case errors.Is(err, domain.ErrInvalid):
		return openapi.ListSharedWithMe400JSONResponse{BadRequestJSONResponse: invalid(ctx)}, nil
	case err != nil:
		return nil, err
	}
	out := openapi.ListSharedWithMe200JSONResponse{
		Items:  make([]openapi.SharedWithMeItem, len(shared.Items)),
		Counts: openapi.SharedWithMeCounts{All: shared.Counts.All, Test: shared.Counts.Test, QuestionSet: shared.Counts.QuestionSet},
		Page:   shared.Page.Number, PageSize: shared.Page.Size, Total: shared.Page.Total,
	}
	for i, item := range shared.Items {
		out.Items[i] = openapi.SharedWithMeItem{
			ShareId: httpapi.ParseUUID(item.ShareID), Kind: openapi.ShareKind(item.Kind), ResourceId: httpapi.ParseUUID(item.ResourceID),
			Name: item.Name, Access: openapi.ShareAccess(item.Access), SharedAt: item.SharedAt.UTC(), QuestionCount: item.QuestionCount,
			Owner: openapi.SharedWithMeOwner{
				Id: httpapi.ParseUUID(item.Owner.ID), Name: item.Owner.Name, AvatarUrl: item.Owner.AvatarURL, Disabled: item.Owner.Disabled,
			},
		}
	}
	return out, nil
}

// RemoveSharedWithMe implements DELETE /teacher/shared-with-me/{shareId}.
func (h Sharing) RemoveSharedWithMe(ctx context.Context, request openapi.RemoveSharedWithMeRequestObject) (openapi.RemoveSharedWithMeResponseObject, error) {
	who, ok := httpapi.ActorFromContext(ctx)
	if h.app == nil || !ok {
		return nil, httpx.ErrNotImplemented
	}
	_, err := h.app.Commands.RemoveSharedWithMe.Handle(ctx, command.RemoveSharedWithMe{Actor: who, ShareID: request.ShareId.String()})
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return openapi.RemoveSharedWithMe404JSONResponse{NotFoundJSONResponse: notFound(ctx)}, nil
	case err != nil:
		return nil, err
	}
	return openapi.RemoveSharedWithMe204Response{}, nil
}
