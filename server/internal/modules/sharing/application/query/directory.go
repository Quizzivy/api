package query

import (
	"context"

	"quizzivy/internal/modules/sharing/application/internal/support"
	"quizzivy/internal/modules/sharing/domain"
)

// Directory is the share dialog's picker: at most Limit colleagues of ActorID
// (domain.MaxDirectory when it is out of bounds) that match Query, or every
// one by name when it is empty.
type Directory struct {
	ActorID string
	Query   string
	Limit   int
}

type DirectoryHandler struct {
	*support.Service
}

func (s DirectoryHandler) Handle(ctx context.Context, q Directory) ([]domain.Person, error) {
	limit := q.Limit
	if limit < 1 || limit > domain.MaxDirectory {
		limit = domain.MaxDirectory
	}
	people, err := s.Repo.Directory(ctx, q.ActorID, q.Query, limit)
	if err != nil {
		return nil, err
	}
	signed := map[string]*string{}
	for i := range people {
		s.SignPerson(ctx, &people[i], signed)
	}
	return people, nil
}
