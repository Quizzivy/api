package support

import (
	"context"

	"quizzivy/internal/modules/questions/domain"
)

// ForTest gives req the owner of the test it is made for, when testID names
// one: the actor must be able to edit the test under the tests module's rule.
// A test it cannot read answers ErrTestNotFound, as a missing one does, and
// one it reads through a Can use grant ErrTestReadOnly.
func (s *Service) ForTest(ctx context.Context, req domain.WriteRequest, testID string) (domain.WriteRequest, error) {
	if testID == "" {
		return req, nil
	}
	if s.Tests == nil {
		return domain.WriteRequest{}, domain.ErrTestNotFound
	}
	owner, err := s.Tests.EditableTest(ctx, req.Scope(), testID)
	if err != nil {
		return domain.WriteRequest{}, err
	}
	req.OwnerID = owner
	return req, nil
}
