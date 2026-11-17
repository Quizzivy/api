package query

import (
	"context"

	"quizzivy/internal/modules/sharing/application/internal/support"
	"quizzivy/internal/modules/sharing/domain"
	"quizzivy/internal/shared/paging"
)

// SharedWithMe reads one page of what other teachers shared with the reader,
// as Filter names it.
type SharedWithMe struct {
	Filter domain.SharedFilter
}

// SharedPage is the page, the counts per kind and where it stands.
type SharedPage struct {
	Items  []domain.SharedItem
	Counts domain.Counts
	Page   paging.Page
}

type SharedWithMeHandler struct {
	*support.Service
}

func (s SharedWithMeHandler) Handle(ctx context.Context, q SharedWithMe) (SharedPage, error) {
	if q.Filter.Kind != "" && !q.Filter.Kind.Valid() {
		return SharedPage{}, domain.ErrInvalid
	}
	items, counts, page, err := s.Repo.SharedWithMe(ctx, q.Filter)
	if err != nil {
		return SharedPage{}, err
	}
	signed := map[string]*string{}
	for i := range items {
		s.SignOwner(ctx, &items[i].Owner, signed)
	}
	return SharedPage{Items: items, Counts: counts, Page: page}, nil
}

// CountSharedWithMe is the number of live grants to UserID, the muted count
// beside "Shared with me".
type CountSharedWithMe struct {
	UserID string
}

type CountSharedWithMeHandler struct {
	*support.Service
}

func (s CountSharedWithMeHandler) Handle(ctx context.Context, q CountSharedWithMe) (int, error) {
	return s.Repo.CountSharedWithMe(ctx, q.UserID)
}
