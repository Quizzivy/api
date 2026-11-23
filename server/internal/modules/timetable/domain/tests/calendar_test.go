package domain_test

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"quizzivy/internal/modules/timetable/domain"
	"quizzivy/internal/shared/validation"
)

// TestACalendarCoversOneToFortyTwoDays reads the local dates [from, to) as
// the instants between their midnights in the caller's zone, a day that is
// not 24 hours long included, and refuses an empty, reversed or longer range
// naming to.
func TestACalendarCoversOneToFortyTwoDays(t *testing.T) {
	hcm := domain.LocationOf("Asia/Ho_Chi_Minh")
	from, to, err := domain.CalendarRange(date("2026-11-23"), date("2027-01-04"), hcm)
	if err != nil {
		t.Fatalf("forty-two days were refused: %v", err)
	}
	if want := time.Date(2026, 11, 23, 0, 0, 0, 0, hcm); !from.Equal(want) {
		t.Errorf("the range starts at %v, want %v", from, want)
	}
	if want := time.Date(2027, 1, 4, 0, 0, 0, 0, hcm); !to.Equal(want) {
		t.Errorf("the range ends at %v, want %v", to, want)
	}

	york := domain.LocationOf("America/New_York")
	from, to, err = domain.CalendarRange(date("2026-11-01"), date("2026-11-02"), york)
	if err != nil {
		t.Fatalf("the day the clocks go back was refused: %v", err)
	}
	if got := to.Sub(from); got != 25*time.Hour {
		t.Errorf("the day the clocks go back in New York lasts %v, want 25h", got)
	}

	if _, _, err := domain.CalendarRange(date("2026-11-23"), date("2026-11-24"), hcm); err != nil {
		t.Errorf("one day was refused: %v", err)
	}
	for name, to := range map[string]string{"no day": "2026-11-23", "a reversed range": "2026-11-22", "forty-three days": "2027-01-05"} {
		_, _, err := domain.CalendarRange(date("2026-11-23"), date(to), hcm)
		var refused *validation.Error
		if !errors.As(err, &refused) || len(refused.Fields) != 1 || refused.Fields[0].Field != "to" {
			t.Errorf("%s answered %v, want a refusal naming to", name, err)
		}
	}
}

// TestTheEventsAreTheOpensAndClosesInTheRange draws each window's opening and
// closing that fall in the range, ordered by time: one opening before the
// range and closing inside it draws only its close, one spanning the range
// draws nothing, and one closed before it opened draws only its close.
func TestTheEventsAreTheOpensAndClosesInTheRange(t *testing.T) {
	from, to := date("2026-11-23"), date("2026-11-30")
	day := func(d int, h int) time.Time { return from.AddDate(0, 0, d).Add(time.Duration(h) * time.Hour) }
	windows := []domain.AssignmentWindow{
		{ID: "before", Title: "Mở trước", OpensAt: day(-3, 8), ClosesAt: day(2, 17), ClassNames: []string{"Lớp A"}},
		{ID: "inside", Title: "Trong tuần", OpensAt: day(1, 8), ClosesAt: day(4, 17)},
		{ID: "spans", Title: "Cả tháng", OpensAt: day(-10, 8), ClosesAt: day(20, 17)},
		{ID: "after", Title: "Mở cuối", OpensAt: day(6, 8), ClosesAt: day(9, 17)},
		{ID: "never", Title: "Đóng sớm", OpensAt: day(5, 8), ClosesAt: day(3, 9)},
		{ID: "edge", Title: "Đóng lúc hết tuần", OpensAt: day(-1, 0), ClosesAt: to},
		{ID: "same", Title: "Mở cùng lúc", OpensAt: day(2, 17), ClosesAt: day(5, 17)},
	}
	type seen struct {
		kind domain.EventKind
		id   string
		at   time.Time
	}
	var got []seen
	for _, e := range domain.Events(windows, from, to) {
		got = append(got, seen{e.Kind, e.AssignmentID, e.At})
		if e.ClassNames == nil {
			t.Errorf("%s %s names no class list", e.Kind, e.AssignmentID)
		}
	}
	want := []seen{
		{domain.EventOpens, "inside", day(1, 8)},
		{domain.EventCloses, "before", day(2, 17)},
		{domain.EventOpens, "same", day(2, 17)},
		{domain.EventCloses, "never", day(3, 9)},
		{domain.EventCloses, "inside", day(4, 17)},
		{domain.EventCloses, "same", day(5, 17)},
		{domain.EventOpens, "after", day(6, 8)},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("the events read\n  %v\nwant\n  %v", got, want)
	}
	if events := domain.Events(nil, from, to); events == nil || len(events) != 0 {
		t.Errorf("no window drew %v, want an empty list", events)
	}
}
