package support

import (
	"context"
	"fmt"
	"time"

	"quizzivy/internal/modules/timetable/domain"
	"quizzivy/internal/shared/access"
)

// RateWindow is what the rates of a register cover at now for a caller in
// loc: the days of the term that holds today there, with the term, or every
// session of the class when no term does.
func (s *Service) RateWindow(ctx context.Context, loc *time.Location, now time.Time) (domain.RateWindow, *domain.TermRef, error) {
	if s.Terms == nil {
		return domain.RateWindow{Overall: true}, nil, nil
	}
	term, err := s.Terms.CurrentTerm(ctx, now.In(loc))
	if err != nil {
		return domain.RateWindow{}, nil, fmt.Errorf("timetable: the current term: %w", err)
	}
	if term == nil {
		return domain.RateWindow{Overall: true}, nil, nil
	}
	return domain.TermWindow(term.StartsOn, term.EndsOn, loc), &domain.TermRef{ID: term.ID, Name: term.Name}, nil
}

// Register reads the register of session id as scope reaches it, for viewerID:
// a one-off's date and the current term read in their zone, and each
// student's photo signed once per key. An id outside scope is
// domain.ErrNotFound.
func (s *Service) Register(ctx context.Context, scope access.Scope, viewerID string, id domain.SessionID) (domain.Register, error) {
	found, err := s.Repo.Find(ctx, scope, id)
	if err != nil {
		return domain.Register{}, err
	}
	manager, err := s.Viewer(ctx, viewerID)
	if err != nil {
		return domain.Register{}, err
	}
	window, term, err := s.RateWindow(ctx, manager.Viewer, s.Now())
	if err != nil {
		return domain.Register{}, err
	}
	students, err := s.Repo.Roster(ctx, found, window)
	if err != nil {
		return domain.Register{}, err
	}
	signed := map[string]*string{}
	for i := range students {
		students[i].AvatarURL = s.sign(ctx, students[i].AvatarKey, signed)
	}
	return domain.Register{Session: manager.Session(found), Term: term, Students: students}, nil
}

func (s *Service) sign(ctx context.Context, key *string, signed map[string]*string) *string {
	if key == nil || *key == "" || s.Avatars == nil {
		return nil
	}
	if url, seen := signed[*key]; seen {
		return url
	}
	var out *string
	if url, err := s.Avatars.AvatarURL(ctx, *key); err == nil && url != "" {
		out = &url
	}
	signed[*key] = out
	return out
}
