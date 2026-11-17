package adapters

import (
	"context"
	"errors"

	questionsquery "quizzivy/internal/modules/questions/application/query"
	questionsdomain "quizzivy/internal/modules/questions/domain"
	sharingdomain "quizzivy/internal/modules/sharing/domain"
	testsquery "quizzivy/internal/modules/tests/application/query"
	testsdomain "quizzivy/internal/modules/tests/domain"
	"quizzivy/internal/shared/access"
	"quizzivy/internal/shared/cqrs"
)

// SharedContent is the tests module's Ownership query and the questions
// module's GetSet behind the sharing module's Content port: each resolves its
// own kind under its own module's read rule, so a grant names only what the
// caller may read there.
type SharedContent struct {
	Tests cqrs.QueryHandler[testsquery.Ownership, testsdomain.Ownership]
	Sets  cqrs.QueryHandler[questionsquery.GetSet, questionsdomain.QuestionSet]
}

// Resource resolves the live test or question set id, as scope reads it.
func (c SharedContent) Resource(ctx context.Context, scope access.Scope, kind sharingdomain.Kind, id string) (sharingdomain.Resource, error) {
	switch kind {
	case sharingdomain.KindTest:
		if c.Tests == nil {
			return sharingdomain.Resource{}, sharingdomain.ErrNotFound
		}
		test, err := c.Tests.Handle(ctx, testsquery.Ownership{ID: id, Scope: scope})
		if errors.Is(err, testsdomain.ErrNotFound) {
			return sharingdomain.Resource{}, sharingdomain.ErrNotFound
		}
		if err != nil {
			return sharingdomain.Resource{}, err
		}
		return sharingdomain.Resource{Kind: kind, ID: test.TestID, Name: test.Title, OwnerID: test.OwnerID}, nil
	case sharingdomain.KindQuestionSet:
		if c.Sets == nil {
			return sharingdomain.Resource{}, sharingdomain.ErrNotFound
		}
		set, err := c.Sets.Handle(ctx, questionsquery.GetSet{Scope: scope, ID: id})
		if errors.Is(err, questionsdomain.ErrSetNotFound) {
			return sharingdomain.Resource{}, sharingdomain.ErrNotFound
		}
		if err != nil {
			return sharingdomain.Resource{}, err
		}
		return sharingdomain.Resource{Kind: kind, ID: set.ID, Name: set.Name, OwnerID: set.OwnerID}, nil
	}
	return sharingdomain.Resource{}, sharingdomain.ErrNotFound
}
