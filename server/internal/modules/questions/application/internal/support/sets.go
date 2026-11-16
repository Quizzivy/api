package support

import (
	"context"

	"quizzivy/internal/modules/questions/domain"
)

// ForTest gives req the owner of the test it is made for, when testID names
// one: the actor must be able to edit the test, and a test it cannot answers
// ErrTestNotFound, as a missing one does.
func (s *Service) ForTest(ctx context.Context, req domain.WriteRequest, testID string) (domain.WriteRequest, error) {
	if testID == "" {
		return req, nil
	}
	if s.Contexts == nil {
		return domain.WriteRequest{}, domain.ErrTestNotFound
	}
	owner, err := s.Contexts.TestOwner(ctx, req.Scope(), testID)
	if err != nil {
		return domain.WriteRequest{}, err
	}
	req.OwnerID = owner
	return req, nil
}
