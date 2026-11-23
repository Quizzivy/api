// Package support is what the timetable's handlers share.
package support

import (
	"context"
	"fmt"
	"time"

	"quizzivy/internal/modules/timetable/application/ports"
	"quizzivy/internal/modules/timetable/domain"
	"quizzivy/internal/shared/actor"
)

// Service carries the store, the terms (nil: no term holds any date), the
// profile zones (nil: every account is read in domain.DefaultZone), the
// photo signer (nil: no student has a photo) and the clock.
type Service struct {
	Repo    domain.Repository
	Terms   ports.Terms
	Zones   ports.Zones
	Avatars ports.Avatars
	Now     func() time.Time
}

// ZoneOf is the zone userID's dates and times are read in, as a name a series
// can store.
func (s *Service) ZoneOf(ctx context.Context, userID string) (string, error) {
	if s.Zones == nil || userID == "" {
		return domain.DefaultZone, nil
	}
	zone, err := s.Zones.ZoneOf(ctx, userID)
	if err != nil {
		return "", fmt.Errorf("timetable: the caller's zone: %w", err)
	}
	return domain.ZoneName(zone), nil
}

// Viewer is the manager that reads one-offs in userID's zone.
func (s *Service) Viewer(ctx context.Context, userID string) (domain.OccurrenceManager, error) {
	zone, err := s.ZoneOf(ctx, userID)
	if err != nil {
		return domain.OccurrenceManager{}, err
	}
	return domain.OccurrenceManager{Viewer: domain.LocationOf(zone)}, nil
}

// TermEnd is the last day of the term that holds day, or nil.
func (s *Service) TermEnd(ctx context.Context, day time.Time) (*time.Time, error) {
	if s.Terms == nil {
		return nil, nil
	}
	term, err := s.Terms.CurrentTerm(ctx, domain.Day(day))
	if err != nil {
		return nil, fmt.Errorf("timetable: the term of %s: %w", domain.Day(day).Format("2006-01-02"), err)
	}
	if term == nil {
		return nil, nil
	}
	end := domain.Day(term.EndsOn)
	return &end, nil
}

// WriterOf is the audit writer of who, now.
func (s *Service) WriterOf(who actor.Actor) domain.Writer {
	return domain.Writer{ActorID: who.ID, IP: who.IP, UserAgent: who.UserAgent, Now: s.Now()}
}
