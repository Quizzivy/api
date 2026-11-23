package domain_test

import (
	"testing"
	"time"

	"quizzivy/internal/modules/timetable/domain"
)

func date(s string) time.Time {
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return d
}

func ptr[T any](v T) *T { return &v }

func weekly(id, zone string, weekday domain.Weekday, start string, from string, until *string) domain.Series {
	clock, err := domain.ParseClock(start)
	if err != nil {
		panic(err)
	}
	s := domain.Series{
		ID: id, ClassID: "class-" + id, ClassName: "Lớp " + id, Weekday: weekday, Start: clock,
		DurationMinutes: 90, Room: ptr("P." + id), StartsOn: date(from), TimeZone: zone,
	}
	if until != nil {
		s.EndsOn = ptr(date(*until))
	}
	return s
}

func local(t *testing.T, zone, at string) time.Time {
	t.Helper()
	loc, err := time.LoadLocation(zone)
	if err != nil {
		t.Fatal(err)
	}
	v, err := time.ParseInLocation("2006-01-02 15:04", at, loc)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func ids(sessions []domain.Session) []string {
	out := make([]string, len(sessions))
	for i, s := range sessions {
		out[i] = s.ID.String()
	}
	return out
}

// TestExpansionWalksMonthAndYearEndsInTheSeriesZone covers a Wednesday series
// across the end of November, December and the year, in Asia/Ho_Chi_Minh
// (UTC+7) and in Asia/Seoul (UTC+9). Each start is the local date at 18:00 in
// the series' zone, and the range is judged in instants.
func TestExpansionWalksMonthAndYearEndsInTheSeriesZone(t *testing.T) {
	for _, zone := range []string{"Asia/Ho_Chi_Minh", "Asia/Seoul"} {
		s := weekly("019535d9-3df7-79fb-b466-fa907fa17f9e", zone, 3, "18:00", "2026-11-18", nil)
		from, to := local(t, zone, "2026-11-23 00:00"), local(t, zone, "2027-01-11 00:00")
		got := domain.OccurrenceManager{}.Expand([]domain.Series{s}, nil, from, to)
		want := []string{"2026-11-25", "2026-12-02", "2026-12-09", "2026-12-16", "2026-12-23", "2026-12-30", "2027-01-06"}
		if len(got) != len(want) {
			t.Fatalf("%s: %d occurrences %v, want %v", zone, len(got), ids(got), want)
		}
		for i, session := range got {
			if session.Date.Format("2006-01-02") != want[i] {
				t.Errorf("%s: occurrence %d on %s, want %s", zone, i, session.Date.Format("2006-01-02"), want[i])
			}
			if wantAt := local(t, zone, want[i]+" 18:00"); !session.StartsAt.Equal(wantAt) {
				t.Errorf("%s: occurrence %d starts %s, want %s", zone, i, session.StartsAt.UTC(), wantAt.UTC())
			}
			if session.ID.String() != s.ID+"."+want[i][0:4]+want[i][5:7]+want[i][8:10] {
				t.Errorf("%s: occurrence %d is %s", zone, i, session.ID)
			}
			if session.Series == nil || session.Room == nil || *session.Room != "P."+s.ID || session.Changed || session.Cancelled {
				t.Errorf("%s: occurrence %d reads %+v", zone, i, session)
			}
		}
	}
}

// TestExpansionKeepsToTheSeriesWindowAndTheInstants bounds a series by its
// first and last dates, and the range by instants: a UTC+9 series' Monday
// 08:00 is Sunday 23:00 UTC, inside a range that ends at Monday 00:00 UTC.
func TestExpansionKeepsToTheSeriesWindowAndTheInstants(t *testing.T) {
	s := weekly("019535d9-3df7-79fb-b466-fa907fa17f9e", "Asia/Seoul", 1, "08:00", "2026-12-07", ptr("2026-12-21"))
	from, to := time.Date(2026, 11, 30, 0, 0, 0, 0, time.UTC), time.Date(2026, 12, 28, 0, 0, 0, 0, time.UTC)
	got := domain.OccurrenceManager{}.Expand([]domain.Series{s}, nil, from, to)
	if want := []string{s.ID + ".20261207", s.ID + ".20261214", s.ID + ".20261221"}; len(got) != 3 || ids(got)[0] != want[0] || ids(got)[2] != want[2] {
		t.Fatalf("occurrences %v, want %v", ids(got), want)
	}
	late := domain.OccurrenceManager{}.Expand([]domain.Series{s}, nil, time.Date(2026, 12, 20, 23, 0, 1, 0, time.UTC), to)
	if len(late) != 0 {
		t.Errorf("a range from one second after the last start holds %v", ids(late))
	}
}

// TestAMovedOccurrenceAppearsOnItsNewDateOnly moves the 2 December occurrence
// to Friday 4 December at 09:00. It is read once, on the 4th, under the id of
// the date it was scheduled for, and the series computes nothing for the 2nd.
// A range that holds only the 2nd holds nothing.
func TestAMovedOccurrenceAppearsOnItsNewDateOnly(t *testing.T) {
	zone := "Asia/Ho_Chi_Minh"
	s := weekly("019535d9-3df7-79fb-b466-fa907fa17f9e", zone, 3, "18:00", "2026-11-18", nil)
	moved := domain.Row{
		ID: "row-1", ClassID: s.ClassID, ClassName: s.ClassName, SeriesID: &s.ID, SeriesDate: ptr(date("2026-12-02")),
		StartsAt: local(t, zone, "2026-12-04 09:00"), DurationMinutes: 90,
	}
	week := domain.OccurrenceManager{}.Expand([]domain.Series{s}, []domain.Row{moved}, local(t, zone, "2026-11-30 00:00"), local(t, zone, "2026-12-07 00:00"))
	if len(week) != 1 {
		t.Fatalf("the week holds %v, want the moved occurrence alone", ids(week))
	}
	got := week[0]
	if got.ID.String() != s.ID+".20261202" || got.Date.Format("2006-01-02") != "2026-12-04" || !got.Changed || got.RowID != "row-1" {
		t.Errorf("the moved occurrence reads id %s, date %s, changed %v, row %q", got.ID, got.Date.Format("2006-01-02"), got.Changed, got.RowID)
	}
	if got.Room == nil || *got.Room != *s.Room {
		t.Errorf("the moved occurrence meets in %v, want the series' room", got.Room)
	}
	if day := (domain.OccurrenceManager{}).Expand([]domain.Series{s}, []domain.Row{moved}, local(t, zone, "2026-12-02 00:00"), local(t, zone, "2026-12-03 00:00")); len(day) != 0 {
		t.Errorf("the date it moved from still holds %v", ids(day))
	}
}

// TestExpansionAddsOneOffsAndSortsByStart reads a one-off's date in the
// viewer's zone and puts it among the occurrences by start.
func TestExpansionAddsOneOffsAndSortsByStart(t *testing.T) {
	zone := "Asia/Ho_Chi_Minh"
	s := weekly("019535d9-3df7-79fb-b466-fa907fa17f9e", zone, 3, "18:00", "2026-11-18", nil)
	oneOff := domain.Row{ID: "019535d9-3df7-79fb-b466-fa907fa17f00", ClassID: s.ClassID, StartsAt: time.Date(2026, 11, 25, 23, 30, 0, 0, time.UTC), DurationMinutes: 60}
	seoul, _ := time.LoadLocation("Asia/Seoul")
	got := domain.OccurrenceManager{Viewer: seoul}.Expand([]domain.Series{s}, []domain.Row{oneOff}, local(t, zone, "2026-11-23 00:00"), local(t, zone, "2026-12-03 00:00"))
	if want := []string{s.ID + ".20261125", oneOff.ID, s.ID + ".20261202"}; len(got) != 3 || ids(got)[0] != want[0] || ids(got)[1] != want[1] || ids(got)[2] != want[2] {
		t.Fatalf("sessions %v, want %v", ids(got), want)
	}
	if got[1].Date.Format("2006-01-02") != "2026-11-26" || got[1].Series != nil || got[1].Changed {
		t.Errorf("the one-off reads date %s in the viewer's zone, series %v, changed %v", got[1].Date.Format("2006-01-02"), got[1].Series, got[1].Changed)
	}
}

// TestAStoredRowOverlaysItsOccurrence reads a row naming an occurrence that
// is not moved: its note and cancellation show, and the series' computed
// occurrence for the date is not added beside it.
func TestAStoredRowOverlaysItsOccurrence(t *testing.T) {
	zone := "Asia/Ho_Chi_Minh"
	s := weekly("019535d9-3df7-79fb-b466-fa907fa17f9e", zone, 3, "18:00", "2026-11-18", nil)
	row := domain.Row{
		ID: "row-1", ClassID: s.ClassID, SeriesID: &s.ID, SeriesDate: ptr(date("2026-11-25")),
		StartsAt: local(t, zone, "2026-11-25 18:00"), DurationMinutes: 90, Note: ptr("Phòng 3"), CancelledAt: ptr(time.Now()), HasAttendance: true,
	}
	got := domain.OccurrenceManager{}.Expand([]domain.Series{s}, []domain.Row{row}, local(t, zone, "2026-11-25 00:00"), local(t, zone, "2026-11-26 00:00"))
	if len(got) != 1 || !got[0].Cancelled || !got[0].Changed || !got[0].HasAttendance || got[0].Note == nil {
		t.Fatalf("the date reads %+v", got)
	}
}
