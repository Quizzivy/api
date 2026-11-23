package domain_test

import (
	"testing"
	"time"

	"quizzivy/internal/modules/timetable/domain"
)

func TestASeriesIsActiveThroughItsLastDayInItsOwnZone(t *testing.T) {
	last := time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC)
	for name, c := range map[string]struct {
		zone   string
		endsOn *time.Time
		now    time.Time
		want   bool
	}{
		"no last date":                       {"Asia/Ho_Chi_Minh", nil, time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC), true},
		"Tokyo, 23:30 on its last day":       {"Asia/Tokyo", &last, time.Date(2026, 11, 22, 14, 30, 0, 0, time.UTC), true},
		"Tokyo, 00:30 the day after":         {"Asia/Tokyo", &last, time.Date(2026, 11, 22, 15, 30, 0, 0, time.UTC), false},
		"Los Angeles, 21:00 on its last day": {"America/Los_Angeles", &last, time.Date(2026, 11, 23, 5, 0, 0, 0, time.UTC), true},
		"Los Angeles, 00:30 the day after":   {"America/Los_Angeles", &last, time.Date(2026, 11, 23, 8, 30, 0, 0, time.UTC), false},
		"the centre's zone, a day before it": {"Asia/Ho_Chi_Minh", &last, time.Date(2026, 11, 21, 3, 0, 0, 0, time.UTC), true},
		"the centre's zone, the week after":  {"Asia/Ho_Chi_Minh", &last, time.Date(2026, 11, 29, 3, 0, 0, 0, time.UTC), false},
	} {
		s := domain.Series{TimeZone: c.zone, StartsOn: last.AddDate(0, -2, 0), EndsOn: c.endsOn}
		if got := s.ActiveOn(c.now); got != c.want {
			t.Errorf("%s: active is %v, want %v", name, got, c.want)
		}
	}
}

func TestASeriesOfAnArchivedClassIsNotActive(t *testing.T) {
	now := time.Date(2026, 11, 23, 3, 0, 0, 0, time.UTC)
	archived := now.Add(-time.Hour)
	s := domain.Series{TimeZone: domain.DefaultZone, StartsOn: now.AddDate(0, -1, 0), ClassArchivedAt: &archived}
	if s.ActiveOn(now) {
		t.Error("an open-ended series of a class archived an hour ago is active, want none: an archived class has no session from its archiving on")
	}
	if s.ClassArchivedAt = nil; !s.ActiveOn(now) {
		t.Error("the same series of the restored class is not active, want it back")
	}
}

func TestASeriesThatHasNotBegunIsActive(t *testing.T) {
	starts := time.Date(2027, 1, 4, 0, 0, 0, 0, time.UTC)
	ends := starts.AddDate(0, 3, 0)
	s := domain.Series{TimeZone: domain.DefaultZone, StartsOn: starts, EndsOn: &ends}
	if !s.ActiveOn(time.Date(2026, 11, 23, 3, 0, 0, 0, time.UTC)) {
		t.Error("a series that starts next term is not active, want it listed on the schedule line now")
	}
}
