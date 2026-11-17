package http

import (
	"context"
	"encoding/json"
	"errors"
	nethttp "net/http"

	"quizzivy/gen/openapi"
	"quizzivy/internal/modules/tests/domain"
	"quizzivy/internal/platform/httpapi"
	"quizzivy/internal/platform/httpx"
)

// refusal is the 403 the contract's ContentAccess table gives a reader of a
// shared test who lacks an action's right: SHARE_READ_ONLY for a Can use
// grant writing, SHARE_OWNER_ONLY for an owner's action. An operation whose own
// responses already name a 403 answers with that type; the others answer
// with this one, as the permission middleware's 403 does.
type refusal struct{ body openapi.ErrorResponse }

// shareRefusal is the refusal err names, if it names one.
func shareRefusal(ctx context.Context, err error) (*refusal, bool) {
	switch {
	case errors.Is(err, domain.ErrReadOnly):
		return &refusal{body: httpapi.Error(ctx, openapi.SHAREREADONLY,
			httpx.Text(ctx, "Bạn chỉ có quyền dùng đề này, không được sửa.", "You can use this test but not change it."))}, true
	case errors.Is(err, domain.ErrOwnerOnly):
		return &refusal{body: httpapi.Error(ctx, openapi.SHAREOWNERONLY,
			httpx.Text(ctx, "Chỉ chủ sở hữu đề mới làm được việc này.", "Only the test's owner can do this."))}, true
	}
	return nil, false
}

func (r *refusal) write(w nethttp.ResponseWriter) error {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(nethttp.StatusForbidden)
	return json.NewEncoder(w).Encode(r.body)
}

func (r *refusal) VisitUpdateTestResponse(w nethttp.ResponseWriter) error    { return r.write(w) }
func (r *refusal) VisitDeleteTestResponse(w nethttp.ResponseWriter) error    { return r.write(w) }
func (r *refusal) VisitDuplicateTestResponse(w nethttp.ResponseWriter) error { return r.write(w) }
func (r *refusal) VisitPublishTestResponse(w nethttp.ResponseWriter) error   { return r.write(w) }
func (r *refusal) VisitDeleteTestVersionResponse(w nethttp.ResponseWriter) error {
	return r.write(w)
}
func (r *refusal) VisitSetCurrentTestVersionResponse(w nethttp.ResponseWriter) error {
	return r.write(w)
}
func (r *refusal) VisitCreateDraftFromTestVersionResponse(w nethttp.ResponseWriter) error {
	return r.write(w)
}
