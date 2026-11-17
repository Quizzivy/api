package http

import (
	"context"
	"encoding/json"
	"errors"
	nethttp "net/http"

	"quizzivy/gen/openapi"
	"quizzivy/internal/modules/questions/domain"
	"quizzivy/internal/platform/httpapi"
	"quizzivy/internal/platform/httpx"
)

// refusal is the 403 the contract's ContentAccess table gives a reader of a
// question shared through a test or a set who lacks an action's right, for
// the operations whose own responses name no 403, as the permission
// middleware's 403 does.
type refusal struct{ body openapi.ErrorResponse }

func questionReadOnly(ctx context.Context) openapi.ErrorResponse {
	return httpapi.Error(ctx, openapi.SHAREREADONLY,
		httpx.Text(ctx, "Bạn chỉ có quyền dùng câu hỏi này, không được sửa.", "You can use this question but not change it."))
}

func testReadOnly(ctx context.Context) openapi.ErrorResponse {
	return httpapi.Error(ctx, openapi.SHAREREADONLY,
		httpx.Text(ctx, "Bạn chỉ có quyền dùng đề này, không được sửa.", "You can use this test but not change it."))
}

// questionRefusal is the refusal err names, if it names one.
func questionRefusal(ctx context.Context, err error) (*refusal, bool) {
	switch {
	case errors.Is(err, domain.ErrTestReadOnly):
		return &refusal{body: testReadOnly(ctx)}, true
	case errors.Is(err, domain.ErrReadOnly):
		return &refusal{body: questionReadOnly(ctx)}, true
	case errors.Is(err, domain.ErrOwnerOnly):
		return &refusal{body: httpapi.Error(ctx, openapi.SHAREOWNERONLY,
			httpx.Text(ctx, "Chỉ chủ sở hữu câu hỏi mới làm được việc này.", "Only the question's owner can do this."))}, true
	}
	return nil, false
}

func (r *refusal) write(w nethttp.ResponseWriter) error {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(nethttp.StatusForbidden)
	return json.NewEncoder(w).Encode(r.body)
}

func (r *refusal) VisitDeleteQuestionResponse(w nethttp.ResponseWriter) error    { return r.write(w) }
func (r *refusal) VisitDuplicateQuestionResponse(w nethttp.ResponseWriter) error { return r.write(w) }
