package query

import (
	"context"

	"quizzivy/internal/modules/messaging/application/internal/support"
	"quizzivy/internal/modules/messaging/domain"
)

// StudentThreads is the student's Messages: their direct threads and the
// channels of their classes that have a post, newest first.
type StudentThreads struct {
	StudentID string
}

type StudentThreadsHandler struct {
	*support.Service
}

func (s StudentThreadsHandler) Handle(ctx context.Context, q StudentThreads) ([]domain.StudentThread, error) {
	threads, err := s.Repo.StudentThreads(ctx, q.StudentID)
	if err != nil {
		return nil, err
	}
	signed := map[string]*string{}
	for i := range threads {
		threads[i].AvatarURL = s.Sign(ctx, threads[i].AvatarKey, signed)
	}
	return threads, nil
}
