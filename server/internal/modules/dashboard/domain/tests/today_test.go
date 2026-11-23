package domain_test

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"quizzivy/internal/modules/dashboard/domain"
)

// TestTheDayRunsFromMidnightToMidnightInTheActorsZone reads the day that
// holds an instant in the actor's zone, the 25-hour day New York's clocks go
// back on included, and in Asia/Ho_Chi_Minh, already the next day there.
func TestTheDayRunsFromMidnightToMidnightInTheActorsZone(t *testing.T) {
	now := time.Date(2026, 11, 1, 20, 0, 0, 0, time.UTC)
	for zone, want := range map[string][2]string{
		"America/New_York": {"2026-11-01T04:00:00Z", "2026-11-02T05:00:00Z"},
		"Asia/Ho_Chi_Minh": {"2026-11-01T17:00:00Z", "2026-11-02T17:00:00Z"},
	} {
		loc, err := time.LoadLocation(zone)
		if err != nil {
			t.Fatal(err)
		}
		from, to := domain.DayOf(now, loc)
		if got := [2]string{from.UTC().Format(time.RFC3339), to.UTC().Format(time.RFC3339)}; got != want {
			t.Errorf("in %s the day runs %v, want %v", zone, got, want)
		}
	}
}

// TestTheDayMergesSessionsWithAssignmentsByTime orders the day by At, an
// assignment before a session at the same instant, each list keeping its own
// order on a tie, and cuts it to forty items.
func TestTheDayMergesSessionsWithAssignmentsByTime(t *testing.T) {
	base := time.Date(2026, 11, 23, 1, 0, 0, 0, time.UTC)
	item := func(kind, id string, hours int) domain.Today {
		out := domain.Today{Kind: kind, At: base.Add(time.Duration(hours) * time.Hour), AssignmentID: id}
		if kind == domain.TodaySessionKind {
			out.AssignmentID, out.Session = "", &domain.TodaySession{ID: id}
		}
		return out
	}
	names := func(in []domain.Today) []string {
		out := make([]string, len(in))
		for i, row := range in {
			out[i] = row.AssignmentID
			if row.Session != nil {
				out[i] = row.Session.ID
			}
		}
		return out
	}
	assignments := []domain.Today{item("opens", "a1", 0), item("closes", "a2", 3), item("opens", "a3", 3)}
	sessions := []domain.Today{item("session", "s1", 1), item("session", "s2", 3), item("session", "s3", 3), item("session", "s4", 9)}
	want := []string{"a1", "s1", "a2", "a3", "s2", "s3", "s4"}
	if got := names(domain.MergeToday(assignments, sessions)); !reflect.DeepEqual(got, want) {
		t.Errorf("the day reads %v, want %v", got, want)
	}

	var many []domain.Today
	for i := range 40 {
		many = append(many, item("session", fmt.Sprintf("s%02d", i), i))
	}
	merged := domain.MergeToday(assignments, many)
	if len(merged) != domain.MaxToday || domain.MaxToday != 40 {
		t.Fatalf("the day holds %d items, want %d", len(merged), domain.MaxToday)
	}
	if last := names(merged)[39]; last != "s36" {
		t.Errorf("the fortieth item is %s, want s36: the latest ones are cut", last)
	}
}
