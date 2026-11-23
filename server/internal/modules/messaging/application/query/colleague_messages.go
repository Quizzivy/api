package query

import (
	"context"

	"quizzivy/internal/modules/messaging/application/internal/support"
	"quizzivy/internal/modules/messaging/domain"
)

// ColleagueThread is one page of ActorID's thread with ColleagueID, empty
// until the first post (F2, T-F2.4). The thread is always the caller's own:
// teacher C naming teacher A reads C's thread with A, never A's thread with
// B, and an Admin reads the Admin's own (D17). An account that is no
// colleague of the caller answers domain.ErrNotFound. No attempt cards and
// no "Seen by".
type ColleagueThread struct {
	ActorID     string
	ColleagueID string
	Cursor      domain.Cursor
}

// ColleagueDirect is the colleague a colleague thread is with and one page
// of it.
type ColleagueDirect struct {
	Peer domain.ColleaguePeer
	Page domain.Page
}

type ColleagueThreadHandler struct {
	*support.Service
}

func (s ColleagueThreadHandler) Handle(ctx context.Context, q ColleagueThread) (ColleagueDirect, error) {
	cursor, err := q.Cursor.Normalize()
	if err != nil {
		return ColleagueDirect{}, err
	}
	facts, err := s.Colleague(ctx, q.ActorID, q.ColleagueID)
	if err != nil {
		return ColleagueDirect{}, err
	}
	peer, err := s.Repo.Colleague(ctx, q.ColleagueID)
	if err != nil {
		return ColleagueDirect{}, err
	}
	page, err := s.Repo.ColleaguePage(ctx, q.ActorID, q.ColleagueID, cursor)
	if err != nil {
		return ColleagueDirect{}, err
	}
	signed := map[string]*string{}
	peer.AvatarURL = s.Sign(ctx, peer.AvatarKey, signed)
	peer.CanPost = s.Rules.CanPostColleague(facts)
	s.SignMessages(ctx, q.ActorID, page.Messages, signed)
	return ColleagueDirect{Peer: peer, Page: page}, nil
}
