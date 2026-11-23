package tests

import (
	"errors"
	"testing"
	"time"

	"quizzivy/internal/modules/organization/domain"
	"quizzivy/internal/shared/validation"
)

func day(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

func semester() domain.Term {
	return domain.Term{ID: "t1", Name: "Học kỳ 1", StartsOn: day(2026, 9, 1), EndsOn: day(2027, 1, 15)}
}

// A term is current on its first day and on its last, both included, and
// nowhere else.
func TestATermsStateTurnsOnItsFirstAndLastDay(t *testing.T) {
	cases := map[string]struct {
		on   time.Time
		want domain.TermState
	}{
		"the day before it starts": {day(2026, 8, 31), domain.TermUpcoming},
		"its first day":            {day(2026, 9, 1), domain.TermCurrent},
		"a day inside":             {day(2026, 11, 20), domain.TermCurrent},
		"its last day":             {day(2027, 1, 15), domain.TermCurrent},
		"the day after it ends":    {day(2027, 1, 16), domain.TermPast},
		"late on its last day":     {time.Date(2027, 1, 15, 23, 59, 59, 0, time.UTC), domain.TermCurrent},
		"years before":             {day(2020, 1, 1), domain.TermUpcoming},
	}
	for name, c := range cases {
		if got := semester().StateOn(c.on); got != c.want {
			t.Errorf("%s (%s): %s, want %s", name, c.on.Format(time.DateOnly), got, c.want)
		}
	}
	oneDay := domain.Term{StartsOn: day(2026, 12, 24), EndsOn: day(2026, 12, 24)}
	for on, want := range map[time.Time]domain.TermState{
		day(2026, 12, 23): domain.TermUpcoming, day(2026, 12, 24): domain.TermCurrent, day(2026, 12, 25): domain.TermPast,
	} {
		if got := oneDay.StateOn(on); got != want {
			t.Errorf("a one-day term on %s: %s, want %s", on.Format(time.DateOnly), got, want)
		}
	}
}

// Today is the calendar day in the viewer's zone: in Ho Chi Minh City (UTC+7)
// the day turns at 17:00 UTC, so a term that starts on 1 September is current
// there while it is still 31 August in UTC.
func TestTodayIsTheDayInTheViewersZone(t *testing.T) {
	hcmc := domain.LocationOf("Asia/Ho_Chi_Minh")
	cases := map[string]struct {
		now  time.Time
		loc  *time.Location
		want domain.TermState
	}{
		"16:59:59 UTC in Ho Chi Minh City is 23:59:59 on 31 August":  {time.Date(2026, 8, 31, 16, 59, 59, 0, time.UTC), hcmc, domain.TermUpcoming},
		"17:00 UTC in Ho Chi Minh City is midnight on 1 September":   {time.Date(2026, 8, 31, 17, 0, 0, 0, time.UTC), hcmc, domain.TermCurrent},
		"17:00 UTC on the last day, in UTC":                          {time.Date(2027, 1, 15, 17, 0, 0, 0, time.UTC), time.UTC, domain.TermCurrent},
		"17:00 UTC on the last day, in Ho Chi Minh City":             {time.Date(2027, 1, 15, 17, 0, 0, 0, time.UTC), hcmc, domain.TermPast},
		"in Los Angeles it is still the last day at 07:59 UTC after": {time.Date(2027, 1, 16, 7, 59, 0, 0, time.UTC), domain.LocationOf("America/Los_Angeles"), domain.TermCurrent},
	}
	for name, c := range cases {
		if got := semester().StateOn(domain.Today(c.now, c.loc)); got != c.want {
			t.Errorf("%s: %s, want %s", name, got, c.want)
		}
	}
}

func TestAZoneTheServerDoesNotKnowCountsInTheCentresZone(t *testing.T) {
	for _, zone := range []string{"", "Local", "Mars/Olympus_Mons"} {
		if got := domain.LocationOf(zone).String(); got != domain.DefaultZone {
			t.Errorf("LocationOf(%q) = %s, want %s", zone, got, domain.DefaultZone)
		}
	}
}

func TestADraftIsTrimmedAndRefusedForABlankNameOrAnEndBeforeItsStart(t *testing.T) {
	got, err := domain.TermDraft{Name: "  Học kỳ 2  ", StartsOn: time.Date(2027, 2, 1, 9, 30, 0, 0, time.UTC), EndsOn: day(2027, 2, 1)}.Normalised()
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Học kỳ 2" || !got.StartsOn.Equal(day(2027, 2, 1)) || !got.EndsOn.Equal(got.StartsOn) {
		t.Errorf("normalised to %+v, want the name trimmed and a one-day term on 2027-02-01", got)
	}

	_, err = domain.TermDraft{Name: " \t ", StartsOn: day(2027, 2, 2), EndsOn: day(2027, 2, 1)}.Normalised()
	var refused *validation.Error
	if !errors.As(err, &refused) {
		t.Fatalf("a blank name ending before its start: %v, want a validation error", err)
	}
	fields := map[string]bool{}
	for _, f := range refused.Fields {
		fields[f.Field] = true
	}
	if !fields["name"] || !fields["endsOn"] || len(fields) != 2 {
		t.Errorf("refused %v, want name and endsOn", fields)
	}
}

func TestAPatchKeepsWhatItLeavesOutAndNamesWhatItChanges(t *testing.T) {
	ends := day(2027, 1, 31)
	draft := domain.TermPatch{EndsOn: &ends}.Over(semester())
	if draft.Name != "Học kỳ 1" || !draft.StartsOn.Equal(day(2026, 9, 1)) || !draft.EndsOn.Equal(ends) {
		t.Errorf("patched to %+v, want only the end moved", draft)
	}
	next := semester()
	next.EndsOn = ends
	if got := semester().ChangedFields(next); len(got) != 1 || got[0] != "endsOn" {
		t.Errorf("changed fields %v, want [endsOn]", got)
	}
	if got := semester().ChangedFields(semester()); len(got) != 0 {
		t.Errorf("an unchanged term names %v", got)
	}
}
