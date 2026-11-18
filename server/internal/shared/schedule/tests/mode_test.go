package schedule_test

import (
	"quizzivy/internal/shared/schedule"
	"strings"
	"testing"
	"time"
)

func TestAModesDeadline(t *testing.T) {
	start := time.Date(2026, 11, 18, 9, 0, 0, 0, time.UTC)
	far, near := start.Add(48*time.Hour), start.Add(30*time.Minute)
	for _, c := range []struct {
		mode   schedule.Mode
		closes time.Time
		want   time.Time
	}{
		{schedule.Strict, far, start.Add(time.Hour)},
		{schedule.Strict, near, near},
		{schedule.Flexible, far, far},
		{schedule.Flexible, near, near},
	} {
		if got := c.mode.Deadline(start, 60, c.closes); !got.Equal(c.want) {
			t.Errorf("%s with the close %v: %v, want %v", c.mode, c.closes, got, c.want)
		}
	}
	if schedule.Mode("").OrStrict() != schedule.Strict || schedule.Flexible.OrStrict() != schedule.Flexible {
		t.Error("OrStrict does not keep a named mode and default an empty one to strict")
	}
	if schedule.Mode("relaxed").Valid() || !schedule.Flexible.Valid() || !schedule.Strict.Valid() {
		t.Error("Valid does not accept exactly strict and flexible")
	}
}

func TestTheDeadlineSQLReadsTheModeAndTheOverride(t *testing.T) {
	sql := schedule.DeadlineOf("o")
	for _, want := range []string{"a.mode = 'flexible'", schedule.CloseOf("o"), "coalesce(o.duration_minutes, a.duration_minutes)"} {
		if !strings.Contains(sql, want) {
			t.Errorf("DeadlineOf lacks %q:\n%s", want, sql)
		}
	}
}
