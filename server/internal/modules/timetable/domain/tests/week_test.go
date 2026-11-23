package domain_test

import (
	"reflect"
	"testing"
	"time"

	"quizzivy/internal/modules/timetable/domain"
)

// TestAWeekStartsOnTheMondayOfTheISOWeek reads every day of a week, the
// Sunday included, as that week's Monday, across a month and a year end.
func TestAWeekStartsOnTheMondayOfTheISOWeek(t *testing.T) {
	for on, want := range map[string]string{
		"2026-11-23": "2026-11-23", "2026-11-26": "2026-11-23", "2026-11-29": "2026-11-23",
		"2026-11-30": "2026-11-30", "2027-01-01": "2026-12-28", "2027-01-03": "2026-12-28",
	} {
		if got := domain.MondayOf(date(on)).Format(time.DateOnly); got != want {
			t.Errorf("the week of %s starts on %s, want %s", on, got, want)
		}
	}
}

func dayItems(w domain.Week) map[string][]string {
	out := map[string][]string{}
	for _, d := range w.Days {
		for _, item := range d.Items {
			key := d.Date.Format(time.DateOnly)
			if item.Session != nil {
				out[key] = append(out[key], "session "+item.Session.ID.String())
			} else {
				out[key] = append(out[key], "due "+item.Due.ID)
			}
		}
	}
	return out
}

// TestAStudentWeekRunsFromMondayToSundayInTheirZone lays out a week in
// Asia/Ho_Chi_Minh (UTC+7): the instants that are Sunday evening in UTC but
// Monday morning in the zone open the week, Sunday 23:59 local closes it, and
// what falls at the next Monday's midnight is the next week's. A session sorts
// before a due item at the same instant, a cancelled session stays, and an
// assignment handed in is left out unless an attempt is under way.
func TestAStudentWeekRunsFromMondayToSundayInTheirZone(t *testing.T) {
	zone := "Asia/Ho_Chi_Minh"
	hcm := domain.LocationOf(zone)
	session := func(id, at string, cancelled bool) domain.Session {
		return domain.Session{ID: domain.OneOffID(id), StartsAt: local(t, zone, at), DurationMinutes: 60, Cancelled: cancelled}
	}
	due := func(id, at string, handedIn, live bool) domain.DueAssignment {
		return domain.DueAssignment{ID: id, Title: "Bài " + id, ClosesAt: local(t, zone, at), HandedIn: handedIn, Live: live}
	}
	sessions := []domain.Session{
		session("before", "2026-11-22 23:59", false),
		session("monday", "2026-11-23 00:00", false),
		session("cancelled", "2026-11-25 18:00", true),
		session("sunday", "2026-11-29 23:59", false),
		session("next", "2026-11-30 00:00", false),
	}
	items := []domain.DueAssignment{
		due("at-the-session", "2026-11-25 18:00", false, false),
		due("early", "2026-11-23 06:00", false, false),
		due("handed-in", "2026-11-26 17:00", true, false),
		due("retaken", "2026-11-27 17:00", true, true),
		due("next-week", "2026-11-30 00:00", false, false),
	}
	week := domain.StudentWeek(date("2026-11-23"), hcm, sessions, items)
	if week.Start.Format(time.DateOnly) != "2026-11-23" || week.End.Format(time.DateOnly) != "2026-11-29" || len(week.Days) != 7 {
		t.Fatalf("the week runs %s to %s over %d days", week.Start.Format(time.DateOnly), week.End.Format(time.DateOnly), len(week.Days))
	}
	for i, d := range week.Days {
		if want := date("2026-11-23").AddDate(0, 0, i); !d.Date.Equal(want) || d.Items == nil {
			t.Errorf("day %d is %s with items %v, want %s and a list", i, d.Date, d.Items, want.Format(time.DateOnly))
		}
	}
	want := map[string][]string{
		"2026-11-23": {"session monday", "due early"},
		"2026-11-25": {"session cancelled", "due at-the-session"},
		"2026-11-27": {"due retaken"},
		"2026-11-29": {"session sunday"},
	}
	if got := dayItems(week); !reflect.DeepEqual(got, want) {
		t.Errorf("the week holds %v, want %v", got, want)
	}
	if !week.Days[2].Items[0].Session.Cancelled {
		t.Error("the cancelled session lost its flag")
	}

	utc := domain.StudentWeek(date("2026-11-23"), time.UTC, sessions, items)
	if got := dayItems(utc)["2026-11-22"]; got != nil {
		t.Errorf("a UTC week holds a Sunday before it: %v", got)
	}
	if got := dayItems(utc)["2026-11-29"]; !reflect.DeepEqual(got, []string{"session sunday", "session next", "due next-week"}) {
		t.Errorf("the UTC Sunday holds %v, want the sessions that are the next Monday in UTC+7", got)
	}
}
