package ports

import "context"

// Avatars signs a GET for the profile photo a student's row names
// (adapters.TeacherPhotos over identity's AvatarURL), and answers an empty
// URL when no store is configured.
type Avatars interface {
	AvatarURL(ctx context.Context, key string) (string, error)
}
