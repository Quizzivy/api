package query

import (
	"context"
	"quizzivy/internal/modules/classes/application/internal/support"
	"quizzivy/internal/modules/classes/domain"
)

// ListMine reads the classes a student belongs to, each with its teacher's
// photo signed and the weekly pattern of its active series, read for every
// class at once. A photo that cannot be signed is left out, not an error.
type ListMine struct {
	UserID string
}

type ListMineHandler struct {
	*support.Service
}

func (s ListMineHandler) Handle(ctx context.Context, q ListMine) ([]domain.MyClass, error) {
	classes, err := s.Repo.ListMine(ctx, q.UserID)
	if err != nil {
		return nil, err
	}
	if err := s.AttachMySchedule(ctx, classes); err != nil {
		return nil, err
	}
	if s.Avatars == nil {
		return classes, nil
	}
	signed := map[string]*string{}
	for i := range classes {
		key := classes[i].TeacherAvatarKey
		if key == nil {
			continue
		}
		url, seen := signed[*key]
		if !seen {
			if signedURL, err := s.Avatars.AvatarURL(ctx, *key); err == nil && signedURL != "" {
				url = &signedURL
			}
			signed[*key] = url
		}
		classes[i].TeacherAvatarURL = url
	}
	return classes, nil
}
