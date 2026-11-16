package query

import (
	"context"

	"quizzivy/internal/modules/messaging/application/internal/support"
	"quizzivy/internal/modules/messaging/domain"
	"quizzivy/internal/shared/access"
)

// StaffDirectThread is one page of ActorID's direct thread with StudentID,
// empty until the first post. A student who is no messaging peer of the
// caller answers domain.ErrNotFound, an admin's thread included.
type StaffDirectThread struct {
	ActorID   string
	StudentID string
	Cursor    domain.Cursor
}

// DirectThread is the student a direct thread is with and one page of it.
type DirectThread struct {
	Peer domain.Peer
	Page domain.Page
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
	signed := map[string]*string{}
	peer.AvatarURL = s.Sign(ctx, peer.AvatarKey, signed)
	peer.CanPost = s.Rules.CanPost(facts)
	s.SignMessages(ctx, q.ActorID, page.Messages, signed)
	return DirectThread{Peer: peer, Page: page}, nil
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
