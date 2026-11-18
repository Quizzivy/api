package domain_test

import (
	"quizzivy/internal/modules/attempts/domain"
	"quizzivy/internal/shared/schedule"
	"testing"
	"time"
)

// F1: under a flexible assignment the deadline is the student's close, the
// time limit (an override's too) is not applied, and an early close counts.
func TestAFlexibleDeadlineIsTheStudentsClose(t *testing.T) {
	now := time.Date(2026, 11, 18, 9, 0, 0, 0, time.UTC)
	closes := now.Add(72 * time.Hour)
	early := now.Add(30 * time.Hour)
	for _, c := range []struct {
		name  string
		rules domain.Rules
		want  time.Time
	}{
		{"a 60-minute limit is not applied", domain.Rules{Mode: schedule.Flexible, DurationMinutes: 60, ClosesAt: closes}, closes},
		{"an early close comes first", domain.Rules{Mode: schedule.Flexible, DurationMinutes: 60, ClosesAt: closes, ClosedAt: &early}, early},
		{"strict keeps the time limit", domain.Rules{Mode: schedule.Strict, DurationMinutes: 60, ClosesAt: closes}, now.Add(time.Hour)},
		{"no mode reads as strict", domain.Rules{DurationMinutes: 60, ClosesAt: closes}, now.Add(time.Hour)},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := c.rules.Deadline(now); !got.Equal(c.want) {
				t.Errorf("deadline %v, want %v", got, c.want)
			}
		})
	}
}

// An override's later close and its own time limit reach the rules through
// schedule.Window.WithOverride; under flexible only the close is read.
func TestAFlexibleDeadlineFollowsAnOverridesCloseAndNotItsTimeLimit(t *testing.T) {
	now := time.Date(2026, 11, 18, 9, 0, 0, 0, time.UTC)
	later, minutes := now.Add(96*time.Hour), 5
	w := schedule.Window{OpensAt: now.Add(-time.Hour), ClosesAt: now.Add(24 * time.Hour), DurationMin: 60, MaxAttempts: 1}.
		WithOverride(&schedule.Override{ClosesAt: &later, DurationMin: &minutes})
	r := domain.Rules{Mode: schedule.Flexible, ClosesAt: w.ClosesAt, ClosedAt: w.ClosedAt, DurationMinutes: w.DurationMin}
	if got := r.Deadline(now); !got.Equal(later) {
		t.Errorf("deadline %v, want the override's close %v", got, later)
	}
	r.Mode = schedule.Strict
	if got := r.Deadline(now); !got.Equal(now.Add(5 * time.Minute)) {
		t.Errorf("strict deadline %v, want the override's five minutes", got)
	}
}

// A flexible start is refused only by a window under way, so the span the
// maintenance check reads ends just after the start; a strict one's ends at
// its deadline.
func TestTheMaintenanceSpanOfAFlexibleStartIsItsInstant(t *testing.T) {
	now := time.Date(2026, 11, 18, 9, 0, 0, 0, time.UTC)
	flexible := domain.Rules{Mode: schedule.Flexible, DurationMinutes: 60, ClosesAt: now.Add(72 * time.Hour)}
	if got := flexible.GuardedUntil(now); !got.Equal(now.Add(time.Microsecond)) {
		t.Errorf("flexible span ends %v, want a microsecond after the start", got)
	}
	strict := flexible
	strict.Mode = schedule.Strict
	if got := strict.GuardedUntil(now); !got.Equal(now.Add(time.Hour)) {
		t.Errorf("strict span ends %v, want the deadline", got)
	}
}
