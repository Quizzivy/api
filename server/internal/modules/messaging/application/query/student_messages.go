package query

import (
	"context"

	"quizzivy/internal/modules/messaging/application/internal/support"
	"quizzivy/internal/modules/messaging/domain"
)

// StudentDirectThread is one page of StudentID's thread with TeacherID, empty
// until the first post: a teacher of a class the student is in now, or one
// they have a thread with. Anyone else answers domain.ErrNotFound.
type StudentDirectThread struct {
	StudentID string
	TeacherID string
	Cursor    domain.Cursor
}

// StudentDirect is the teacher a student's direct thread is with and one page
// of it, as the student reads it.
type StudentDirect struct {
	Teacher domain.Teacher
	Page    domain.StudentPage
}

type StudentDirectThreadHandler struct {
	*support.Service
}

func (s StudentDirectThreadHandler) Handle(ctx context.Context, q StudentDirectThread) (StudentDirect, error) {
	cursor, err := q.Cursor.Normalize()
	if err != nil {
		return StudentDirect{}, err
	}
	facts, err := s.AsStudent(ctx, q.StudentID, q.TeacherID)
	if err != nil {
		return StudentDirect{}, err
	}
	teacher, err := s.Repo.Teacher(ctx, q.StudentID, q.TeacherID)
	if err != nil {
		return StudentDirect{}, err
	}
	page, err := s.Repo.DirectPage(ctx, q.TeacherID, q.StudentID, cursor)
	if err != nil {
		return StudentDirect{}, err
	}
	signed := map[string]*string{}
	teacher.AvatarURL = s.Sign(ctx, teacher.AvatarKey, signed)
	teacher.CanPost = s.Rules.CanPostAsStudent(facts)
	out := page.ForStudent()
	s.SignStudentMessages(ctx, q.StudentID, out.Messages, signed)
	return StudentDirect{Teacher: teacher, Page: out}, nil
}

// StudentClassThread is one page of the channel of ClassID for a student in
// the class, archived or not; anyone else gets domain.ErrNotFound. Its posts
// carry no "Seen by".
type StudentClassThread struct {
	StudentID string
	ClassID   string
	Cursor    domain.Cursor
}

// StudentClass is a class's channel as its student reads it and one page of
// it.
type StudentClass struct {
	Channel domain.StudentChannel
	Page    domain.StudentPage
}

type StudentClassThreadHandler struct {
	*support.Service
}

func (s StudentClassThreadHandler) Handle(ctx context.Context, q StudentClassThread) (StudentClass, error) {
	cursor, err := q.Cursor.Normalize()
	if err != nil {
		return StudentClass{}, err
	}
	channel, err := s.Repo.StudentChannel(ctx, q.StudentID, q.ClassID)
	if err != nil {
		return StudentClass{}, err
	}
	page, err := s.Repo.StudentChannelPage(ctx, q.ClassID, cursor)
	if err != nil {
		return StudentClass{}, err
	}
	signed := map[string]*string{}
	channel.Teacher.AvatarURL = s.Sign(ctx, channel.Teacher.AvatarKey, signed)
	out := page.ForStudent()
	s.SignStudentMessages(ctx, q.StudentID, out.Messages, signed)
	return StudentClass{Channel: channel, Page: out}, nil
}
