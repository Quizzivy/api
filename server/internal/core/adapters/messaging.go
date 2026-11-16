package adapters

import (
	"context"

	identityquery "quizzivy/internal/modules/identity/application/query"
	"quizzivy/internal/shared/cqrs"
)

// MessagingPhotos is identity's AvatarURL query behind the messaging module's
// Avatars port: it signs the photo of a thread's student and of each
// message's sender, staff or student alike, and answers an empty URL when the
// account has none or no store is configured.
type MessagingPhotos struct {
	Query cqrs.QueryHandler[identityquery.AvatarURL, string]
}

// AvatarURL signs a GET for the stored photo key.
func (a MessagingPhotos) AvatarURL(ctx context.Context, key string) (string, error) {
	return a.Query.Handle(ctx, identityquery.AvatarURL{Key: &key})
}
