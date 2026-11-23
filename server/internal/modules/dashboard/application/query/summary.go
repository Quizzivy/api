package query

import (
	"context"
	"fmt"
	"quizzivy/internal/modules/dashboard/application/internal/support"
	"quizzivy/internal/modules/dashboard/domain"
	timetablequery "quizzivy/internal/modules/timetable/application/query"
	"quizzivy/internal/shared/access"
	"time"
)

// Summary reads the teacher's home over what Scope reaches; a zero Scope
// reads nothing.
type Summary struct {
	Scope            access.Scope
	Range            string
	CanReviewFlagged bool
}

type SummaryHandler struct {
	*support.Service
}

func (s SummaryHandler) Handle(ctx context.Context, q Summary) (domain.Summary, error) {
	zone := domain.DefaultZone
	if s.Zones != nil {
		var err error
		zone, err = s.Zones.ZoneOf(ctx, q.Scope.UserID)
		if err != nil {
			return domain.Summary{}, err
		}
	}
	now := s.Now()
	home, err := s.Repo.Home(ctx, domain.HomeQuery{Scope: q.Scope, Now: now, Zone: zone, Days: daysOf(q.Range)})
	if err != nil {
		return domain.Summary{}, err
	}
	sessions, err := s.todaySessions(ctx, q.Scope, zone, now)
	if err != nil {
		return domain.Summary{}, err
	}
	home.Today = domain.MergeToday(home.Today, sessions)
	out, err := s.Repo.Summary(ctx, domain.SummaryQuery{Scope: q.Scope, CanReviewFlagged: q.CanReviewFlagged})
	out.Home = home
	return out, err
}

// todaySessions are the sessions of the classes scope reaches that start
// today in zone and are not cancelled, from the timetable, in its order.
// Without the timetable there are none.
func (s SummaryHandler) todaySessions(ctx context.Context, scope access.Scope, zone string, now time.Time) ([]domain.Today, error) {
	if s.Sessions == nil {
		return nil, nil
	}
	from, to := domain.DayOf(now, locationOf(zone))
	sessions, err := s.Sessions.Handle(ctx, timetablequery.Occurrences{Scope: scope, ViewerID: scope.UserID, From: from, To: to})
	if err != nil {
		return nil, fmt.Errorf("dashboard: today's sessions: %w", err)
	}
	out := make([]domain.Today, 0, len(sessions))
	for _, session := range sessions {
		if session.Cancelled {
			continue
		}
		out = append(out, domain.Today{Kind: domain.TodaySessionKind, At: session.StartsAt, Session: &domain.TodaySession{
			ID: session.ID.String(), ClassID: session.ClassID, ClassName: session.ClassName, Room: session.Room,
		}})
	}
	return out, nil
}

// locationOf is zone, or the centre's default zone when this server cannot
// load it, the reading the repository's calendar figures make of it.
func locationOf(zone string) *time.Location {
	if loc, err := time.LoadLocation(zone); err == nil && zone != "" {
		return loc
	}
	if loc, err := time.LoadLocation(domain.DefaultZone); err == nil {
		return loc
	}
	return time.FixedZone(domain.DefaultZone, 7*60*60)
}

func daysOf(value string) int {
	switch value {
	case "7d":
		return 7
	case "30d":
		return 30
	default:
		return 14
	}
}
