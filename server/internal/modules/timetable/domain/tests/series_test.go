package domain_test

import (
	"errors"
	"testing"
	"time"

	"quizzivy/internal/modules/timetable/domain"
	"quizzivy/internal/shared/validation"
)

func refusedField(t *testing.T, err error) string {
	t.Helper()
	var invalid *validation.Error
	if !errors.As(err, &invalid) || len(invalid.Fields) != 1 {
		t.Fatalf("got %v, want one refused field", err)
	}
	return invalid.Fields[0].Field
}

// TestASplitWithAttendanceTodayStartsTomorrow changes a Monday series from
// today, a Monday whose occurrence already has attendance: the change starts
// tomorrow, the old series ends today, and the new one runs from tomorrow to
// the old last date.
func TestASplitWithAttendanceTodayStartsTomorrow(t *testing.T) {
	s := weekly("old", "Asia/Ho_Chi_Minh", 1, "18:00", "2026-09-07", ptr("2027-01-11"))
	later := domain.Clock{Hour: 19, Minute: 30}
	today := date("2026-11-23")
	split, err := s.SplitAt(domain.SeriesChange{Start: &later}, today, today, true)
	if err != nil {
		t.Fatal(err)
	}
	if split.InPlace || !split.From.Equal(date("2026-11-24")) || !split.Old.EndsOn.Equal(today) {
		t.Fatalf("the split is from %s, in place %v, the old series ending %s", split.From, split.InPlace, split.Old.EndsOn)
	}
	if !split.Next.StartsOn.Equal(date("2026-11-24")) || !split.Next.EndsOn.Equal(date("2027-01-11")) || split.Next.Start != later || split.Next.ID != "" {
		t.Errorf("the new series is %+v", split.Next)
	}
	if len(split.Fields) != 1 || split.Fields[0] != "startTime" {
		t.Errorf("the split names %v", split.Fields)
	}
	plain, err := s.SplitAt(domain.SeriesChange{Start: &later}, today, today, false)
	if err != nil || !plain.From.Equal(today) || !plain.Old.EndsOn.Equal(date("2026-11-22")) {
		t.Errorf("without attendance today the split is from %s, the old series ending %v (%v)", plain.From, plain.Old.EndsOn, err)
	}
}

// TestASplitRefusesThePastAndAfterTheEnd names fromDate for a date before
// today in the series' zone, and for one after its last date, including the
// day attendance pushes it to.
func TestASplitRefusesThePastAndAfterTheEnd(t *testing.T) {
	s := weekly("old", "Asia/Ho_Chi_Minh", 1, "18:00", "2026-09-07", ptr("2026-11-23"))
	longer := 120
	today := date("2026-11-23")
	for name, from := range map[string]time.Time{"yesterday": date("2026-11-22"), "after the end": date("2026-11-30")} {
		_, err := s.SplitAt(domain.SeriesChange{DurationMinutes: &longer}, from, today, false)
		if got := refusedField(t, err); got != "fromDate" {
			t.Errorf("%s refused %s", name, got)
		}
	}
	_, err := s.SplitAt(domain.SeriesChange{DurationMinutes: &longer}, today, today, true)
	if got := refusedField(t, err); got != "fromDate" {
		t.Errorf("a last day with attendance refused %s", got)
	}
}

// TestASplitBeforeTheFirstDateChangesTheSeriesInPlace keeps the series when
// none of it has come yet, and names no field when nothing changes.
func TestASplitBeforeTheFirstDateChangesTheSeriesInPlace(t *testing.T) {
	s := weekly("old", "Asia/Ho_Chi_Minh", 1, "18:00", "2026-12-07", nil)
	friday := domain.Weekday(5)
	split, err := s.SplitAt(domain.SeriesChange{Weekday: &friday, Room: domain.TextPatch{Set: true}}, date("2026-11-23"), date("2026-11-23"), false)
	if err != nil {
		t.Fatal(err)
	}
	if !split.InPlace || split.Next.ID != "old" || split.Next.Weekday != 5 || split.Next.Room != nil || !split.Next.StartsOn.Equal(s.StartsOn) || split.Old.EndsOn != nil {
		t.Errorf("the split is %+v", split)
	}
	same := s.Weekday
	unchanged, err := s.SplitAt(domain.SeriesChange{Weekday: &same}, date("2026-11-23"), date("2026-11-23"), false)
	if err != nil || len(unchanged.Fields) != 0 {
		t.Errorf("a change to the same weekday names %v (%v)", unchanged.Fields, err)
	}
}

// TestEndingBeforeTheStartRemovesTheSeries removes a series whose last date
// is before its first, ends it on any other, and refuses one more than 731
// days after its first date.
func TestEndingBeforeTheStartRemovesTheSeries(t *testing.T) {
	s := weekly("old", "Asia/Ho_Chi_Minh", 1, "18:00", "2026-12-07", nil)
	if end, err := s.EndOn(date("2026-12-06")); err != nil || !end.Remove {
		t.Errorf("the day before the first date: %+v, %v", end, err)
	}
	if end, err := s.EndOn(date("2026-12-07")); err != nil || end.Remove || !end.LastDate.Equal(date("2026-12-07")) {
		t.Errorf("the first date: %+v, %v", end, err)
	}
	if end, err := s.EndOn(date("2028-12-07")); err != nil || end.Remove {
		t.Errorf("731 days on: %+v, %v", end, err)
	}
	_, err := s.EndOn(date("2028-12-08"))
	if got := refusedField(t, err); got != "lastDate" {
		t.Errorf("732 days on refused %s", got)
	}
}

// TestADraftIsRefusedNamingItsField refuses a date over two years ahead, an
// end without a repeat, an end before the date and an end over 731 days on.
func TestADraftIsRefusedNamingItsField(t *testing.T) {
	today := date("2026-11-23")
	for name, c := range map[string]struct {
		draft domain.Draft
		field string
	}{
		"two years and a day":   {domain.Draft{Date: date("2028-11-24")}, "date"},
		"an end without repeat": {domain.Draft{Date: today, EndsOn: ptr(date("2026-12-31"))}, "endsOn"},
		"an end before":         {domain.Draft{Date: today, RepeatWeekly: true, EndsOn: ptr(date("2026-11-22"))}, "endsOn"},
		"an end too far":        {domain.Draft{Date: today, RepeatWeekly: true, EndsOn: ptr(date("2028-11-24"))}, "endsOn"},
	} {
		if got := refusedField(t, c.draft.Validate(today)); got != c.field {
			t.Errorf("%s refused %s, want %s", name, got, c.field)
		}
	}
	if err := (domain.Draft{Date: date("2028-11-23"), RepeatWeekly: true, EndsOn: ptr(date("2028-11-23"))}).Validate(today); err != nil {
		t.Errorf("two years to the day: %v", err)
	}
}

// TestARepeatRunsToTheEndOfItsTerm takes the draft's weekday and first date,
// its own end, else the term's last day, else none.
func TestARepeatRunsToTheEndOfItsTerm(t *testing.T) {
	d := domain.Draft{Date: date("2026-11-25"), Start: domain.Clock{Hour: 18}, DurationMinutes: 90, RepeatWeekly: true}
	term := date("2027-01-15")
	s := d.Series("class", "Asia/Seoul", &term, ptr("P.1"))
	if s.Weekday != 3 || !s.StartsOn.Equal(date("2026-11-25")) || s.EndsOn == nil || !s.EndsOn.Equal(term) || s.TimeZone != "Asia/Seoul" || *s.Room != "P.1" {
		t.Errorf("the series is %+v", s)
	}
	if open := d.Series("class", "Asia/Seoul", nil, nil); open.EndsOn != nil {
		t.Errorf("without a term the series ends %s", open.EndsOn)
	}
	d.EndsOn = ptr(date("2026-12-31"))
	if own := d.Series("class", "Asia/Seoul", &term, nil); own.EndsOn == nil || !own.EndsOn.Equal(date("2026-12-31")) {
		t.Errorf("with its own end the series ends %s", own.EndsOn)
	}
}

// TestCancelAndRestore cancels an occurrence without a row, which stores one,
// and restores it, which leaves the row nothing to hold: it is not needed. A
// second restore changes nothing.
func TestCancelAndRestore(t *testing.T) {
	zone := "Asia/Ho_Chi_Minh"
	s := weekly("019535d9-3df7-79fb-b466-fa907fa17f9e", zone, 3, "18:00", "2026-11-18", nil)
	now := local(t, zone, "2026-11-23 10:00")
	id := domain.OccurrenceID(s.ID, date("2026-11-25"))
	yes, no := true, false
	cancel, err := domain.Patch{Cancelled: &yes}.Apply(domain.Found{ID: id, Series: &s}, nil, now)
	if err != nil || !cancel.Store || !cancel.Changed || !cancel.Cancelled || cancel.Fields.CancelledAt == nil {
		t.Fatalf("cancelling: %+v, %v", cancel, err)
	}
	row := domain.Row{ID: "row", ClassID: s.ClassID, SeriesID: &s.ID, SeriesDate: ptr(date("2026-11-25")),
		StartsAt: cancel.Fields.StartsAt, DurationMinutes: cancel.Fields.DurationMinutes, CancelledAt: cancel.Fields.CancelledAt}
	if !cancel.Fields.StartsAt.Equal(local(t, zone, "2026-11-25 18:00")) {
		t.Errorf("the cancelled row starts %s", cancel.Fields.StartsAt)
	}
	restore, err := domain.Patch{Cancelled: &no}.Apply(domain.Found{ID: id, Series: &s, Row: &row}, nil, now)
	if err != nil || restore.Store || !restore.Changed || restore.Cancelled || restore.Fields.CancelledAt != nil {
		t.Fatalf("restoring: %+v, %v", restore, err)
	}
	again, err := domain.Patch{Cancelled: &no}.Apply(domain.Found{ID: id, Series: &s}, nil, now)
	if err != nil || again.Changed {
		t.Errorf("restoring a session that is not cancelled: %+v, %v", again, err)
	}
	row.HasAttendance = true
	if kept, _ := (domain.Patch{Cancelled: &no}).Apply(domain.Found{ID: id, Series: &s, Row: &row}, nil, now); !kept.Store {
		t.Errorf("restoring a session with attendance drops its row")
	}
}

// TestAMoveKeepsTheTimeAndReadsTheSeriesZone moves an occurrence to another
// date and time, read in the series' zone whatever the viewer's, keeps its
// local start time when only the date moves, and refuses a date over two
// years ahead.
func TestAMoveKeepsTheTimeAndReadsTheSeriesZone(t *testing.T) {
	zone := "Asia/Seoul"
	s := weekly("019535d9-3df7-79fb-b466-fa907fa17f9e", zone, 3, "18:00", "2026-11-18", nil)
	now := local(t, zone, "2026-11-23 10:00")
	id := domain.OccurrenceID(s.ID, date("2026-11-25"))
	viewer, _ := time.LoadLocation("America/New_York")
	moved, err := domain.Patch{Date: ptr(date("2026-11-27"))}.Apply(domain.Found{ID: id, Series: &s}, viewer, now)
	if err != nil || !moved.Store || !moved.Fields.StartsAt.Equal(local(t, zone, "2026-11-27 18:00")) {
		t.Fatalf("moving: %+v, %v", moved, err)
	}
	later, err := domain.Patch{Date: ptr(date("2026-11-27")), Start: &domain.Clock{Hour: 19}}.Apply(domain.Found{ID: id, Series: &s}, viewer, now)
	if err != nil || !later.Fields.StartsAt.Equal(local(t, zone, "2026-11-27 19:00")) {
		t.Errorf("moving to 19:00 on the 27th starts %s, want 19:00 in %s (%v)", later.Fields.StartsAt, zone, err)
	}
	_, err = domain.Patch{Date: ptr(date("2028-11-24"))}.Apply(domain.Found{ID: id, Series: &s}, viewer, now)
	if got := refusedField(t, err); got != "date" {
		t.Errorf("a move over two years ahead refused %s", got)
	}
}
