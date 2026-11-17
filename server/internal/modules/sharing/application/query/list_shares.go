package query

import (
	"context"

	"quizzivy/internal/modules/sharing/application/internal/support"
	"quizzivy/internal/modules/sharing/domain"
	"quizzivy/internal/shared/access"
)

// ListShares reads who has access to the test or question set Kind and
// ResourceID name, for its owner or scope.all. A grantee gets
// domain.ErrOwnerOnly, and anyone who cannot read it domain.ErrNotFound.
type ListShares struct {
	Scope      access.Scope
	Kind       domain.Kind
	ResourceID string
}

// Listing is the share dialog's "People with access": the owner, then the
// grants, oldest first.
type Listing struct {
	Owner  domain.Person
	Grants []domain.Grant
}

type ListSharesHandler struct {
	*support.Service
}

func (s ListSharesHandler) Handle(ctx context.Context, q ListShares) (Listing, error) {
	res, standing, err := s.Standing(ctx, q.Scope, q.Kind, q.ResourceID)
	if err != nil {
		return Listing{}, err
	}
	if err := standing.Manage(); err != nil {
		return Listing{}, err
	}
	owner, err := s.Repo.Person(ctx, res.OwnerID)
	if err != nil {
		return Listing{}, err
	}
	grants, err := s.Repo.Grants(ctx, q.Kind, q.ResourceID)
	if err != nil {
		return Listing{}, err
	}
	signed := map[string]*string{}
	s.SignPerson(ctx, &owner, signed)
	for i := range grants {
		s.SignPerson(ctx, &grants[i].Grantee, signed)
	}
	return Listing{Owner: owner, Grants: grants}, nil
}
