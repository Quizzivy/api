package query

import (
	"context"

	"quizzivy/internal/modules/messaging/application/internal/support"
	"quizzivy/internal/modules/messaging/domain"
	"quizzivy/internal/shared/access"
)

// StaffDirectThread is one page of ActorID's direct thread with StudentID,
// empty until the first post, and the student's flagged attempts on the
// assignments Scope reaches. The thread is always the caller's own: a staff
// member who does not teach the student reads their own thread with them,
// never the teacher's (D17). An account that is no messaging peer of the
// caller answers domain.ErrNotFound.
type StaffDirectThread struct {
	ActorID   string
	Scope     access.Scope
	StudentID string
	Cursor    domain.Cursor
}

// DirectThread is the student a direct thread is with, one page of it, and
// the context events shown beside its messages.
type DirectThread struct {
	Peer   domain.Peer
	Page   domain.Page
	Events []domain.ContextEvent
}

type StaffDirectThreadHandler struct {
	*support.Service
}

func (s StaffDirectThreadHandler) Handle(ctx context.Context, q StaffDirectThread) (DirectThread, error) {
	cursor, err := q.Cursor.Normalize()
	if err != nil {
		return DirectThread{}, err
	}
	facts, err := s.Direct(ctx, q.ActorID, q.StudentID)
	if err != nil {
		return DirectThread{}, err
	}
	peer, err := s.Repo.Peer(ctx, q.ActorID, q.StudentID)
	if err != nil {
		return DirectThread{}, err
	}
	page, err := s.Repo.DirectPage(ctx, q.ActorID, q.StudentID, cursor)
	if err != nil {
		return DirectThread{}, err
	}
	events, err := contextEvents(ctx, s.Service, q.Scope, q.ActorID, q.StudentID)
	if err != nil {
		return DirectThread{}, err
	}
	signed := map[string]*string{}
	peer.AvatarURL = s.Sign(ctx, peer.AvatarKey, signed)
	peer.CanPost = s.Rules.CanPost(facts)
	s.SignMessages(ctx, q.ActorID, page.Messages, signed)
	return DirectThread{Peer: peer, Page: page, Events: events}, nil
}

// StaffClassThread is one page of the channel of ClassID, for a reader whose
// class scope reaches it, archived or not; anyone else gets
// domain.ErrNotFound.
type StaffClassThread struct {
	ReaderID string
	Scope    access.Scope
	ClassID  string
	Cursor   domain.Cursor
}

// ClassThread is a class's channel and one page of it, each post with its
// SeenBy.
type ClassThread struct {
	Channel domain.Channel
	Page    domain.Page
}

type StaffClassThreadHandler struct {
	*support.Service
}

func (s StaffClassThreadHandler) Handle(ctx context.Context, q StaffClassThread) (ClassThread, error) {
	cursor, err := q.Cursor.Normalize()
	if err != nil {
		return ClassThread{}, err
	}
	channel, err := s.Repo.Channel(ctx, q.Scope, q.ClassID)
	if err != nil {
		return ClassThread{}, err
	}
	page, err := s.Repo.ChannelPage(ctx, q.ClassID, cursor)
	if err != nil {
		return ClassThread{}, err
	}
	s.SignMessages(ctx, q.ReaderID, page.Messages, map[string]*string{})
	return ClassThread{Channel: channel, Page: page}, nil
}
