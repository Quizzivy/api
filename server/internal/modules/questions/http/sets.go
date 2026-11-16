package http

import (
	"context"
	"errors"
	"fmt"

	"quizzivy/gen/openapi"
	"quizzivy/internal/modules/questions/application/command"
	"quizzivy/internal/modules/questions/application/query"
	"quizzivy/internal/modules/questions/domain"
	"quizzivy/internal/platform/httpapi"
	"quizzivy/internal/platform/httpx"
	"quizzivy/internal/shared/access"
)

func msgSetNotFound(ctx context.Context) string {
	return httpx.Text(ctx, "Không tìm thấy bộ câu hỏi.", "The question set was not found.")
}

func setNotFound(ctx context.Context) openapi.NotFoundJSONResponse {
	return openapi.NotFoundJSONResponse(httpapi.NotFound(ctx, msgSetNotFound(ctx)))
}

func setReadOnly(ctx context.Context) openapi.ErrorResponse {
	return httpapi.Error(ctx, openapi.SHAREREADONLY,
		httpx.Text(ctx, "Bạn chỉ có quyền dùng bộ câu hỏi này, không được sửa.", "You can use this question set but not change it."))
}

func setOwnerOnly(ctx context.Context) openapi.ErrorResponse {
	return httpapi.Error(ctx, openapi.SHAREOWNERONLY,
		httpx.Text(ctx, "Chỉ chủ sở hữu bộ câu hỏi mới làm được việc này.", "Only the question set's owner can do this."))
}

func setInvalid(ctx context.Context, invalid *domain.ValidationError) openapi.ErrorResponse {
	return httpapi.Invalid(ctx, httpx.Text(ctx, "Dữ liệu bộ câu hỏi không hợp lệ.", "The question set data is not valid."), invalid)
}

func setQuestionsNotFound(ctx context.Context, missing *domain.QuestionsNotFoundError) openapi.ErrorResponse {
	ids := make([]openapi.Uuid, len(missing.IDs))
	for i, id := range missing.IDs {
		ids[i] = httpapi.ParseUUID(id)
	}
	return httpapi.ErrorWithDetails(ctx, openapi.NOTFOUND,
		httpx.Text(ctx, "Không tìm thấy một số câu hỏi trong ngân hàng.", "Some questions were not found in the bank."),
		map[string]interface{}{"questionIds": ids})
}

func setWrite(ctx context.Context, id string) (domain.SetWrite, bool) {
	actor, ok := httpapi.ActorFromContext(ctx)
	return domain.SetWrite{ID: id, ActorID: actor.ID, All: actor.Scope.All, IP: actor.IP, UserAgent: actor.UserAgent}, ok
}

func uuidStrings(ids []openapi.Uuid) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = id.String()
	}
	return out
}

func toAPISet(set domain.QuestionSet) openapi.QuestionSet {
	return openapi.QuestionSet{
		Id:            httpapi.ParseUUID(set.ID),
		Name:          set.Name,
		Description:   set.Description,
		Owner:         openapi.PersonRef{Id: httpapi.ParseUUID(set.OwnerID), Name: set.OwnerName},
		Access:        openapi.ContentAccess(set.Access),
		QuestionCount: set.QuestionCount,
		SharedWith:    set.SharedWith,
		CreatedAt:     set.CreatedAt,
		UpdatedAt:     set.UpdatedAt,
	}
}

func rowAccess(set domain.QuestionSet, q domain.Question, scope access.Scope) openapi.ContentAccess {
	if scope.All || q.Owner != nil && q.Owner.ID == scope.UserID {
		return openapi.ContentAccess(domain.AccessOwner)
	}
	return openapi.ContentAccess(set.Access)
}

// ListQuestionSets implements GET /teacher/question-sets: the caller's own sets.
func (h Questions) ListQuestionSets(ctx context.Context, _ openapi.ListQuestionSetsRequestObject) (openapi.ListQuestionSetsResponseObject, error) {
	if h.app == nil || h.app.Queries.ListSets == nil {
		return nil, httpx.ErrNotImplemented
	}
	sets, err := h.app.Queries.ListSets.Handle(ctx, query.ListSets{Scope: httpapi.ScopeFromContext(ctx).Own()})
	if err != nil {
		return nil, err
	}
	out := openapi.ListQuestionSets200JSONResponse{Items: make([]openapi.QuestionSetSummary, len(sets))}
	for i, set := range sets {
		out.Items[i] = openapi.QuestionSetSummary{
			Id: httpapi.ParseUUID(set.ID), Name: set.Name, Description: set.Description,
			QuestionCount: set.QuestionCount, SharedWith: set.SharedWith, UpdatedAt: set.UpdatedAt,
		}
	}
	return out, nil
}

func (h Questions) CreateQuestionSet(ctx context.Context, request openapi.CreateQuestionSetRequestObject) (openapi.CreateQuestionSetResponseObject, error) {
	if h.app == nil || h.app.Commands.CreateSet == nil || request.Body == nil {
		return nil, httpx.ErrNotImplemented
	}
	w, ok := setWrite(ctx, "")
	if !ok {
		return nil, httpx.ErrNotImplemented
	}
	set, err := h.app.Commands.CreateSet.Handle(ctx, command.CreateSet{
		Write: w, Name: request.Body.Name, Description: request.Body.Description, QuestionIDs: uuidStrings(request.Body.QuestionIds),
	})
	var invalid *domain.ValidationError
	var missing *domain.QuestionsNotFoundError
	switch {
	case err == nil:
		return openapi.CreateQuestionSet201JSONResponse(toAPISet(set)), nil
	case errors.As(err, &invalid):
		return openapi.CreateQuestionSet400JSONResponse{BadRequestJSONResponse: openapi.BadRequestJSONResponse(setInvalid(ctx, invalid))}, nil
	case errors.As(err, &missing):
		return openapi.CreateQuestionSet404JSONResponse(setQuestionsNotFound(ctx, missing)), nil
	default:
		return nil, err
	}
}

func (h Questions) GetQuestionSet(ctx context.Context, request openapi.GetQuestionSetRequestObject) (openapi.GetQuestionSetResponseObject, error) {
	if h.app == nil || h.app.Queries.GetSet == nil {
		return nil, httpx.ErrNotImplemented
	}
	set, err := h.app.Queries.GetSet.Handle(ctx, query.GetSet{ID: request.Id.String(), Scope: httpapi.ScopeFromContext(ctx)})
	switch {
	case err == nil:
		return openapi.GetQuestionSet200JSONResponse(toAPISet(set)), nil
	case errors.Is(err, domain.ErrSetNotFound):
		return openapi.GetQuestionSet404JSONResponse{NotFoundJSONResponse: setNotFound(ctx)}, nil
	default:
		return nil, err
	}
}

func (h Questions) UpdateQuestionSet(ctx context.Context, request openapi.UpdateQuestionSetRequestObject) (openapi.UpdateQuestionSetResponseObject, error) {
	if h.app == nil || h.app.Commands.UpdateSet == nil || request.Body == nil {
		return nil, httpx.ErrNotImplemented
	}
	w, ok := setWrite(ctx, request.Id.String())
	if !ok {
		return nil, httpx.ErrNotImplemented
	}
	set, err := h.app.Commands.UpdateSet.Handle(ctx, command.UpdateSet{Write: w, Fields: domain.SetFields{
		Name: request.Body.Name, Description: request.Body.Description, DescriptionSet: request.Body.Description != nil,
	}})
	var invalid *domain.ValidationError
	switch {
	case err == nil:
		return openapi.UpdateQuestionSet200JSONResponse(toAPISet(set)), nil
	case errors.As(err, &invalid):
		return openapi.UpdateQuestionSet400JSONResponse{BadRequestJSONResponse: openapi.BadRequestJSONResponse(setInvalid(ctx, invalid))}, nil
	case errors.Is(err, domain.ErrSetNotFound):
		return openapi.UpdateQuestionSet404JSONResponse{NotFoundJSONResponse: setNotFound(ctx)}, nil
	case errors.Is(err, domain.ErrSetReadOnly):
		return openapi.UpdateQuestionSet403JSONResponse(setReadOnly(ctx)), nil
	default:
		return nil, err
	}
}

func (h Questions) DeleteQuestionSet(ctx context.Context, request openapi.DeleteQuestionSetRequestObject) (openapi.DeleteQuestionSetResponseObject, error) {
	if h.app == nil || h.app.Commands.DeleteSet == nil {
		return nil, httpx.ErrNotImplemented
	}
	w, ok := setWrite(ctx, request.Id.String())
	if !ok {
		return nil, httpx.ErrNotImplemented
	}
	_, err := h.app.Commands.DeleteSet.Handle(ctx, command.DeleteSet{Write: w})
	switch {
	case err == nil:
		return openapi.DeleteQuestionSet204Response{}, nil
	case errors.Is(err, domain.ErrSetNotFound):
		return openapi.DeleteQuestionSet404JSONResponse{NotFoundJSONResponse: setNotFound(ctx)}, nil
	case errors.Is(err, domain.ErrSetOwnerOnly):
		return openapi.DeleteQuestionSet403JSONResponse(setOwnerOnly(ctx)), nil
	default:
		return nil, err
	}
}

func (h Questions) AddQuestionSetItems(ctx context.Context, request openapi.AddQuestionSetItemsRequestObject) (openapi.AddQuestionSetItemsResponseObject, error) {
	if h.app == nil || h.app.Commands.AddToSet == nil || request.Body == nil {
		return nil, httpx.ErrNotImplemented
	}
	w, ok := setWrite(ctx, request.Id.String())
	if !ok {
		return nil, httpx.ErrNotImplemented
	}
	set, err := h.app.Commands.AddToSet.Handle(ctx, command.AddToSet{Write: w, QuestionIDs: uuidStrings(request.Body.QuestionIds)})
	var invalid *domain.ValidationError
	var missing *domain.QuestionsNotFoundError
	switch {
	case err == nil:
		return openapi.AddQuestionSetItems200JSONResponse(toAPISet(set)), nil
	case errors.Is(err, domain.ErrSetFull):
		return openapi.AddQuestionSetItems422JSONResponse(httpapi.FieldError(ctx, "questionIds", httpx.Text(ctx,
			fmt.Sprintf("Một bộ câu hỏi chứa tối đa %d câu.", domain.MaxSetQuestions),
			fmt.Sprintf("A set holds at most %d questions.", domain.MaxSetQuestions)))), nil
	case errors.As(err, &invalid):
		return openapi.AddQuestionSetItems400JSONResponse{BadRequestJSONResponse: openapi.BadRequestJSONResponse(setInvalid(ctx, invalid))}, nil
	case errors.Is(err, domain.ErrSetNotFound):
		return openapi.AddQuestionSetItems404JSONResponse(httpapi.NotFound(ctx, msgSetNotFound(ctx))), nil
	case errors.As(err, &missing):
		return openapi.AddQuestionSetItems404JSONResponse(setQuestionsNotFound(ctx, missing)), nil
	case errors.Is(err, domain.ErrSetOwnerOnly):
		return openapi.AddQuestionSetItems403JSONResponse(setOwnerOnly(ctx)), nil
	default:
		return nil, err
	}
}

func (h Questions) RemoveQuestionSetItems(ctx context.Context, request openapi.RemoveQuestionSetItemsRequestObject) (openapi.RemoveQuestionSetItemsResponseObject, error) {
	if h.app == nil || h.app.Commands.RemoveFromSet == nil || request.Body == nil {
		return nil, httpx.ErrNotImplemented
	}
	w, ok := setWrite(ctx, request.Id.String())
	if !ok {
		return nil, httpx.ErrNotImplemented
	}
	set, err := h.app.Commands.RemoveFromSet.Handle(ctx, command.RemoveFromSet{Write: w, QuestionIDs: uuidStrings(request.Body.QuestionIds)})
	var invalid *domain.ValidationError
	switch {
	case err == nil:
		return openapi.RemoveQuestionSetItems200JSONResponse(toAPISet(set)), nil
	case errors.As(err, &invalid):
		return openapi.RemoveQuestionSetItems400JSONResponse{BadRequestJSONResponse: openapi.BadRequestJSONResponse(setInvalid(ctx, invalid))}, nil
	case errors.Is(err, domain.ErrSetNotFound):
		return openapi.RemoveQuestionSetItems404JSONResponse{NotFoundJSONResponse: setNotFound(ctx)}, nil
	case errors.Is(err, domain.ErrSetReadOnly):
		return openapi.RemoveQuestionSetItems403JSONResponse(setReadOnly(ctx)), nil
	default:
		return nil, err
	}
}
