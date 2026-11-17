package adapters

import (
	"context"
	"errors"

	questionsdomain "quizzivy/internal/modules/questions/domain"
	testsquery "quizzivy/internal/modules/tests/application/query"
	testsdomain "quizzivy/internal/modules/tests/domain"
	"quizzivy/internal/shared/access"
	"quizzivy/internal/shared/cqrs"
)

// EditableTests is the tests module's Ownership query behind the questions
// module's EditableTests port: a bank write made for a test (forTestId) is
// decided by the tests module's own predicate, read with the caller's access
// and held to its edit rule (testsdomain.ActionEdit).
type EditableTests struct {
	Tests cqrs.QueryHandler[testsquery.Ownership, testsdomain.Ownership]
}

// EditableTest returns the owner of the live test testID when scope may edit
// it; questionsdomain.ErrTestNotFound for a test it cannot read, and
// questionsdomain.ErrTestReadOnly for one it reads through a Can use grant.
func (e EditableTests) EditableTest(ctx context.Context, scope access.Scope, testID string) (string, error) {
	if e.Tests == nil {
		return "", questionsdomain.ErrTestNotFound
	}
	test, err := e.Tests.Handle(ctx, testsquery.Ownership{ID: testID, Scope: scope})
	if errors.Is(err, testsdomain.ErrNotFound) {
		return "", questionsdomain.ErrTestNotFound
	}
	if err != nil {
		return "", err
	}
	if err := test.Access.Allows(testsdomain.ActionEdit); err != nil {
		return "", questionsdomain.ErrTestReadOnly
	}
	return test.OwnerID, nil
}
