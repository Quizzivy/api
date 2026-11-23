package domain_test

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"quizzivy/internal/modules/timetable/domain"
)

// TestTheRateIsAWholePercentRoundedHalfUp pins the rate's arithmetic: late
// counts as attended, a half rounds up, and no counted mark is no rate.
func TestTheRateIsAWholePercentRoundedHalfUp(t *testing.T) {
	for _, c := range []struct {
		attended, counted int
		want              *int
	}{
		{0, 0, nil},
		{0, 1, ptr(0)},
		{1, 1, ptr(100)},
		{1, 8, ptr(13)}, // 12.5
		{1, 3, ptr(33)}, // 33.3
		{2, 3, ptr(67)}, // 66.7
		{5, 8, ptr(63)}, // 62.5
		{3, 8, ptr(38)}, // 37.5
		{199, 200, ptr(100)},
		{1, 200, ptr(1)}, // 0.5
	} {
		got := domain.Tally{Attended: c.attended, Counted: c.counted}.Rate()
		if (got == nil) != (c.want == nil) || got != nil && *got != *c.want {
			t.Errorf("%d of %d: rate %v, want %v", c.attended, c.counted, deref(got), deref(c.want))
		}
	}
	if !domain.Late.Attended() || !domain.Present.Attended() || domain.Absent.Attended() {
		t.Error("late and present count as attended, absent does not")
	}
}

func deref(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}

func mark(student string, status *domain.Status, note *string, noteSet bool) domain.MarkInput {
	return domain.MarkInput{StudentID: student, Status: status, Note: domain.TextPatch{Set: noteSet, Value: note}}
}

func refusal(t *testing.T, err error) *domain.RefusedMarks {
	t.Helper()
	var refused *domain.RefusedMarks
	if !errors.As(err, &refused) {
		t.Fatalf("want marks refused, got %v", err)
	}
	return refused
}

// TestCleanMarksTrimsNotesAndRefusesNamingTheStudents trims and composes a
// note, reads a blank one as a clear, and refuses a student named twice, a
// note beside a null status and a note over 500 characters once composed,
// each naming only the students concerned.
func TestCleanMarksTrimsNotesAndRefusesNamingTheStudents(t *testing.T) {
	present := ptr(domain.Present)
	got, err := domain.CleanMarks([]domain.MarkInput{
		mark("a", present, ptr("  Đi muộn vì mưa  "), true),
		mark("b", present, ptr("   "), true),
		mark("c", nil, nil, true),
		mark("d", present, nil, false),
		mark("e", ptr(domain.Late), ptr("Café"), true),
	})
	if err != nil {
		t.Fatal(err)
	}
	if *got[0].Note.Value != "Đi muộn vì mưa" || got[1].Note.Value != nil || !got[1].Note.Set || got[3].Note.Set || *got[4].Note.Value != "Café" {
		t.Errorf("cleaned %+v", got)
	}

	refused := refusal(t, func() error {
		_, err := domain.CleanMarks([]domain.MarkInput{mark("a", present, nil, false), mark("b", present, nil, false), mark("a", nil, nil, false), mark("a", present, nil, false)})
		return err
	}())
	if refused.Reason != domain.RefusedTwice || !slices.Equal(refused.StudentIDs, []string{"a"}) {
		t.Errorf("a student named three times: %+v", refused)
	}
	refused = refusal(t, func() error {
		_, err := domain.CleanMarks([]domain.MarkInput{mark("a", nil, ptr("Ốm"), true), mark("b", nil, nil, true), mark("c", present, ptr("Ốm"), true)})
		return err
	}())
	if refused.Reason != domain.RefusedNoteWithoutStatus || !slices.Equal(refused.StudentIDs, []string{"a"}) {
		t.Errorf("a note beside a null status: %+v", refused)
	}
	// U+0958 is a composition exclusion: NFC writes it as two code points, so
	// 300 of them are 600 characters once composed.
	refused = refusal(t, func() error {
		_, err := domain.CleanMarks([]domain.MarkInput{mark("a", present, ptr(strings.Repeat("x", 500)), true), mark("b", present, ptr(strings.Repeat("क़", 300)), true)})
		return err
	}())
	if refused.Reason != domain.RefusedNoteTooLong || !slices.Equal(refused.StudentIDs, []string{"b"}) {
		t.Errorf("a note over 500 characters: %+v", refused)
	}
	if domain.Changes([]domain.MarkInput{mark("a", nil, nil, true)}) || !domain.Changes([]domain.MarkInput{mark("a", nil, nil, false), mark("b", present, nil, false)}) {
		t.Error("only a mark with a status changes a session with no mark")
	}
}

// TestASessionTakesAttendanceFromFifteenMinutesBeforeItStarts refuses a
// session that starts more than 15 minutes from now and a cancelled one.
func TestASessionTakesAttendanceFromFifteenMinutesBeforeItStarts(t *testing.T) {
	now := time.Date(2026, 11, 23, 3, 0, 0, 0, time.UTC)
	row := func(starts time.Time, cancelled bool) domain.Found {
		r := domain.Row{ID: "r", StartsAt: starts, DurationMinutes: 60}
		if cancelled {
			r.CancelledAt = &now
		}
		return domain.Found{ID: domain.OneOffID("r"), Row: &r}
	}
	for _, c := range []struct {
		name string
		f    domain.Found
		want error
	}{
		{"started an hour ago", row(now.Add(-time.Hour), false), nil},
		{"starting in 15 minutes", row(now.Add(15*time.Minute), false), nil},
		{"starting in 15 minutes and a second", row(now.Add(15*time.Minute+time.Second), false), domain.ErrNotStarted},
		{"next week", row(now.AddDate(0, 0, 7), false), domain.ErrNotStarted},
		{"cancelled yesterday's", row(now.AddDate(0, 0, -1), true), domain.ErrCancelled},
	} {
		if err := c.f.TakesAttendance(now); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", c.name, err, c.want)
		}
	}
	s := weekly("s", "Asia/Ho_Chi_Minh", 1, "10:10", "2026-11-02", nil)
	occurrence := domain.Found{ID: domain.OccurrenceID("s", date("2026-11-23")), Series: &s}
	if err := occurrence.TakesAttendance(now.Add(-5 * time.Minute)); err != nil {
		t.Errorf("an occurrence at 10:10 at 09:55 in its zone: %v", err)
	}
	if err := occurrence.TakesAttendance(now.Add(-6 * time.Minute)); !errors.Is(err, domain.ErrNotStarted) {
		t.Errorf("an occurrence at 10:10 at 09:54 in its zone: %v", err)
	}
}

func listed(id string, starts time.Time, marked bool) domain.Session {
	return domain.Session{ID: domain.OneOffID(id), StartsAt: starts, HasAttendance: marked}
}

func pageIDs(p domain.SessionPage) []string {
	out := make([]string, len(p.Items))
	for i, s := range p.Items {
		out[i] = s.ID.String()
	}
	return out
}

// TestTheRegisterListPagesNewestFirstWithoutSplittingATie keeps the started,
// uncancelled sessions newest first, bounds them by since (inclusive) and
// before (exclusive) and by marked, counts notMarked over every started
// session whatever the filter, and never ends a page inside a tie.
func TestTheRegisterListPagesNewestFirstWithoutSplittingATie(t *testing.T) {
	now := time.Date(2026, 11, 23, 3, 0, 0, 0, time.UTC)
	h := func(n int) time.Time { return now.Add(-time.Duration(n) * time.Hour) }
	cancelled := listed("x-cancelled", h(1), false)
	cancelled.Cancelled = true
	all := []domain.Session{
		listed("a", h(5), true), listed("b", h(4), false), listed("c", h(3), false), listed("d", h(3), true),
		listed("e", h(2), false), cancelled, listed("f", now, false), listed("g", now.Add(time.Second), false),
	}
	started := domain.Started(all, now)
	if got := pageIDs(domain.PageOf(started, domain.SessionFilter{Limit: 50})); !slices.Equal(got, []string{"f", "e", "d", "c", "b", "a"}) {
		t.Fatalf("started, newest first: %v", got)
	}

	page := domain.PageOf(started, domain.SessionFilter{Limit: 3})
	if got := pageIDs(page); !slices.Equal(got, []string{"f", "e", "d", "c"}) || !page.NextBefore.Equal(h(3)) || page.NotMarked != 4 {
		t.Errorf("a page of 3 ending inside a tie: %v next %v notMarked %d", got, page.NextBefore, page.NotMarked)
	}
	page = domain.PageOf(started, domain.SessionFilter{Limit: 3, Before: page.NextBefore})
	if got := pageIDs(page); !slices.Equal(got, []string{"b", "a"}) || page.NextBefore != nil || page.NotMarked != 4 {
		t.Errorf("the next page: %v next %v notMarked %d", got, page.NextBefore, page.NotMarked)
	}
	page = domain.PageOf(started, domain.SessionFilter{Limit: 1})
	if got := pageIDs(page); !slices.Equal(got, []string{"f"}) || !page.NextBefore.Equal(now) {
		t.Errorf("limit 1, the latest: %v next %v", got, page.NextBefore)
	}

	since, before := h(4), h(2)
	page = domain.PageOf(started, domain.SessionFilter{Since: &since, Before: &before})
	if got := pageIDs(page); !slices.Equal(got, []string{"d", "c", "b"}) || page.NextBefore != nil || page.NotMarked != 4 {
		t.Errorf("since inclusive, before exclusive: %v next %v notMarked %d", got, page.NextBefore, page.NotMarked)
	}
	for marked, want := range map[bool][]string{true: {"d", "a"}, false: {"f", "e", "c", "b"}} {
		page = domain.PageOf(started, domain.SessionFilter{Marked: &marked})
		if got := pageIDs(page); !slices.Equal(got, want) || page.NotMarked != 4 {
			t.Errorf("marked %v: %v notMarked %d", marked, got, page.NotMarked)
		}
	}
	if empty := domain.PageOf(nil, domain.SessionFilter{}); empty.Items == nil || len(empty.Items) != 0 || empty.NextBefore != nil {
		t.Errorf("no session: %+v", empty)
	}
}

// TestARegisterCountsItsRosterByMark counts present, late, absent and
// unmarked over the roster.
func TestARegisterCountsItsRosterByMark(t *testing.T) {
	r := domain.Register{Students: []domain.RosterStudent{
		{Status: ptr(domain.Present)}, {Status: ptr(domain.Late)}, {Status: ptr(domain.Late)}, {Status: ptr(domain.Absent)}, {}, {},
	}}
	if got := r.Counts(); got != (domain.RosterCounts{Present: 1, Late: 2, Absent: 1, Unmarked: 2}) {
		t.Errorf("counts %+v", got)
	}
	w := domain.TermWindow(date("2026-09-01"), date("2026-12-31"), time.FixedZone("ICT", 7*3600))
	if !w.From.Equal(time.Date(2026, 8, 31, 17, 0, 0, 0, time.UTC)) || !w.To.Equal(time.Date(2026, 12, 31, 17, 0, 0, 0, time.UTC)) || w.Overall {
		t.Errorf("a term's window %+v", w)
	}
}

// TestABlankNoteBesideANullStatusIsNoNote reads a note that is blank once
// trimmed as no note, beside a null status as beside a status.
func TestABlankNoteBesideANullStatusIsNoNote(t *testing.T) {
	got, err := domain.CleanMarks([]domain.MarkInput{mark("a", nil, ptr("   "), true), mark("b", ptr(domain.Present), ptr(" "), true)})
	if err != nil {
		t.Fatalf("a blank note beside a null status: %v", err)
	}
	if got[0].Note.Value != nil || got[1].Note.Value != nil || !got[0].Note.Set {
		t.Errorf("cleaned %+v", got)
	}
}

// TestASessionFromTheClassArchivingOnDoesNotExist archives a Monday series'
// class at noon on 20 November: the 16th stays, the 23rd and a one-off on the
// 21st are left out, and the row that moved the 30th to the 19th stays while
// hiding nothing it should not. A session found by id answers the same.
func TestASessionFromTheClassArchivingOnDoesNotExist(t *testing.T) {
	archived := local(t, "Asia/Ho_Chi_Minh", "2026-11-20 12:00")
	s := weekly("s", "Asia/Ho_Chi_Minh", 1, "08:00", "2026-11-02", nil)
	s.ClassArchivedAt = &archived
	moved := domain.Row{ID: "moved", ClassID: s.ClassID, SeriesID: &s.ID, SeriesDate: ptr(date("2026-11-30")),
		StartsAt: local(t, "Asia/Ho_Chi_Minh", "2026-11-19 08:00"), DurationMinutes: 90, ClassArchivedAt: &archived}
	late := domain.Row{ID: "late", ClassID: s.ClassID, SeriesID: &s.ID, SeriesDate: ptr(date("2026-11-09")),
		StartsAt: local(t, "Asia/Ho_Chi_Minh", "2026-11-25 08:00"), DurationMinutes: 90, ClassArchivedAt: &archived}
	oneOff := domain.Row{ID: "one", ClassID: s.ClassID, StartsAt: local(t, "Asia/Ho_Chi_Minh", "2026-11-21 08:00"), DurationMinutes: 60, ClassArchivedAt: &archived}
	from := local(t, "Asia/Ho_Chi_Minh", "2026-11-01 00:00")
	got := domain.OccurrenceManager{}.Expand([]domain.Series{s}, []domain.Row{moved, late, oneOff}, from, from.AddDate(0, 1, 0))
	if want := []string{"s.20261102", "s.20261116", "s.20261130"}; !slices.Equal(ids(got), want) {
		t.Errorf("after archiving the month holds %v, want %v", ids(got), want)
	}
	if !got[2].StartsAt.Equal(moved.StartsAt) {
		t.Errorf("the moved occurrence starts %s", got[2].StartsAt)
	}
	for name, c := range map[string]struct {
		f    domain.Found
		want bool
	}{
		"the 16th":                {domain.Found{ID: domain.OccurrenceID("s", date("2026-11-16")), Series: &s}, true},
		"the 23rd":                {domain.Found{ID: domain.OccurrenceID("s", date("2026-11-23")), Series: &s}, false},
		"the 9th, moved after":    {domain.Found{ID: domain.OccurrenceID("s", date("2026-11-09")), Series: &s, Row: &late}, false},
		"the one-off of the 21st": {domain.Found{ID: domain.OneOffID("one"), Row: &oneOff}, false},
	} {
		if got := c.f.Exists(); got != c.want {
			t.Errorf("%s exists %v, want %v", name, got, c.want)
		}
	}
	s.ClassArchivedAt = nil
	if restored := (domain.Found{ID: domain.OccurrenceID("s", date("2026-11-23")), Series: &s}); !restored.Exists() {
		t.Error("the 23rd of a restored class does not exist")
	}
}
