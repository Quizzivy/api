package ports

import (
	"context"
	"quizzivy/internal/shared/access"
)

// MediaKinds answers the kind of an asset the scope may read, as the media
// module defines a readable asset, or ErrMediaNotFound alike for a missing,
// deleted or unreadable one; the media module provides it.
type MediaKinds interface {
	Kind(ctx context.Context, scope access.Scope, assetID string) (string, error)
}

// EditableTests resolves the test a bank write is made for (forTestId) under
// the tests module's own edit rule: the owner of the live test testID when the
// scope may edit it, who owns the question made for it;
// domain.ErrTestNotFound, alike for a missing test and one the scope cannot
// read, and domain.ErrTestReadOnly for a test it reads through a Can use
// grant. The tests module provides it.
type EditableTests interface {
	EditableTest(ctx context.Context, scope access.Scope, testID string) (string, error)
}
