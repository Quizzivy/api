// Package ports is what the messaging context needs from outside it: the
// signed URL of a profile photo, for the students and staff its threads name.
package ports

import "context"

// Avatars signs the URL of a profile photo from the key a user row holds, and
// answers an empty URL when there is none to show.
type Avatars interface {
	AvatarURL(ctx context.Context, key string) (string, error)
}
