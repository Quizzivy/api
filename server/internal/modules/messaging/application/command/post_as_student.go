package command

import (
	"context"

	"quizzivy/internal/modules/messaging/application/internal/support"
	"quizzivy/internal/modules/messaging/domain"
)

// PostAsStudent is a student's message to TeacherID, text only: students
// attach no file. The first post makes the thread, which a student may start
// only with the teacher of a class they are in now; a repeated ClientID
// returns the message the first call made. A teacher the student may not
// name answers domain.ErrNotFound, and a pair that no longer qualifies
// domain.ErrNotAllowed.
type PostAsStudent struct {
	StudentID string
	TeacherID string
	ClientID  string
	Body      string
}

type PostAsStudentHandler struct {
	*support.Service
}

func (s PostAsStudentHandler) Handle(ctx context.Context, cmd PostAsStudent) (domain.StudentPosted, error) {
	content, err := domain.NewContent(&cmd.Body, nil)
	if err != nil {
		return domain.StudentPosted{}, err
	}
	posted, err := s.Repo.PostAsStudent(ctx, cmd.StudentID, cmd.TeacherID, domain.Post{SenderID: cmd.StudentID, ClientID: cmd.ClientID, Content: content})
	if err != nil {
		return domain.StudentPosted{}, err
	}
	s.Ring(ctx, posted)
	out := domain.StudentPosted{Message: posted.Message.ForStudent(), Created: posted.Created}
	s.SignStudentMessage(ctx, cmd.StudentID, &out.Message, map[string]*string{})
	return out, nil
}
