package query

import (
	"context"
	"quizzivy/internal/modules/questions/application/internal/support"
	"quizzivy/internal/modules/questions/domain"
	"quizzivy/internal/shared/access"
	"quizzivy/internal/shared/paging"
)

// List reads one page of the bank Input names. With Input.SetID it reads that
// set's questions instead, once Reader, the caller's whole scope, can read
// the set; a set it cannot answers ErrSetNotFound.
type List struct {
	Input  domain.ListInput
	Reader access.Scope
}

// ListResult is the page, and the set it was read from when there was one.
type ListResult struct {
	Items []domain.Question
	Page  paging.Page
	Set   *domain.QuestionSet
}

type ListHandler struct {
	*support.Service
}

func (s ListHandler) Handle(ctx context.Context, q List) (ListResult, error) {
	var set *domain.QuestionSet
	if q.Input.SetID != "" {
		if s.Sets == nil {
			return ListResult{}, domain.ErrSetNotFound
		}
		found, err := s.Sets.GetSet(ctx, q.Reader, q.Input.SetID)
		if err != nil {
			return ListResult{}, err
		}
		set = &found
	}
	r0, r1, err := s.Repo.List(ctx, q.Input)
	return ListResult{Items: r0, Page: r1, Set: set}, err
}
