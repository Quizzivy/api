//go:build integration

package application_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"quizzivy/internal/modules/timetable/application/command"
	"quizzivy/internal/modules/timetable/application/query"
	"quizzivy/internal/modules/timetable/domain"
	"quizzivy/internal/shared/validation"
)

func (w *world) create(who, class string, d domain.Draft) domain.Session {
	w.t.Helper()
	s, err := w.app.Commands.CreateSession.Handle(context.Background(), command.CreateSession{Actor: by(who), ClassID: class, Draft: d})
	if err != nil {
		w.t.Fatalf("creating a session in %s: %v", class, err)
	}
	return s
}

func (w *world) patch(who, id string, p domain.Patch) domain.Session {
	w.t.Helper()
	s, err := w.app.Commands.UpdateSession.Handle(context.Background(), command.UpdateSession{Actor: by(who), SessionID: id, Patch: p})
	if err != nil {
		w.t.Fatalf("patching %s: %v", id, err)
	}
	return s
}

func (w *world) week(who string, from string) []domain.Session {
	w.t.Helper()
	start := at(from + " 00:00")
	got, err := w.app.Queries.Occurrences.Handle(context.Background(), query.Occurrences{
		Scope: by(who).Scope, ViewerID: who, From: start, To: start.AddDate(0, 0, 7),
	})
	if err != nil {
		w.t.Fatal(err)
	}
	return got
}

func weekly(date, start string) domain.Draft {
	c, _ := domain.ParseClock(start)
	return domain.Draft{Date: day(date), Start: c, DurationMinutes: 90, RepeatWeekly: true}
}

func once(date, start string) domain.Draft {
	d := weekly(date, start)
	d.RepeatWeekly = false
	return d
}

// TestARepeatRunsToTheEndOfTheTermInTheClassRoom starts a Wednesday series
// whose first date a term holds: it ends on the term's last day, meets in the
// class's own room, keeps the caller's zone and answers its first
// occurrence, with one session.created row about the series.
func TestARepeatRunsToTheEndOfTheTermInTheClassRoom(t *testing.T) {
	w := newWorld(t)
	w.terms["2026-09-01"] = day("2027-01-15")
	w.zones[w.a] = "Asia/Seoul"
	class := w.class(w.a, ptr("P.204"))
	got := w.create(w.a, class, weekly("2026-11-25", "18:00"))
	if got.Series == nil || got.Series.EndsOn == nil || !got.Series.EndsOn.Equal(day("2027-01-15")) || got.Series.TimeZone != "Asia/Seoul" {
		t.Fatalf("the series is %+v", got.Series)
	}
	if got.ID.String() != got.Series.ID+".20261125" || got.Room == nil || *got.Room != "P.204" || got.Series.Weekday != 3 {
		t.Errorf("the first occurrence reads %+v", got)
	}
	if want := time.Date(2026, 11, 25, 18, 0, 0, 0, domain.LocationOf("Asia/Seoul")); !got.StartsAt.Equal(want) || got.ClassName == "" {
		t.Errorf("the first occurrence starts %s (%q), want %s", got.StartsAt, got.ClassName, want)
	}
	audit := w.audited(w.a)
	if len(audit) != 1 || audit[0].Action != "session.created" || audit[0].Entity != "class_session_series" || audit[0].EntityID != got.Series.ID {
		t.Fatalf("the audit holds %+v", audit)
	}
	if audit[0].Diff["endsOn"] != "2027-01-15" || audit[0].Diff["room"] != "P.204" {
		t.Errorf("the audit diff is %v", audit[0].Diff)
	}
	open := w.create(w.a, class, weekly("2027-02-03", "18:00"))
	if open.Series.EndsOn != nil {
		t.Errorf("a series no term holds ends %s", open.Series.EndsOn)
	}
}

// TestAOneOffIsReadInTheCallersZone stores a one-off with no room at the
// caller's local time, and answers it under its row's id.
func TestAOneOffIsReadInTheCallersZone(t *testing.T) {
	w := newWorld(t)
	class := w.class(w.a, ptr("P.204"))
	d := once("2026-11-27", "07:30")
	d.Room = domain.TextPatch{Set: true}
	got := w.create(w.a, class, d)
	if got.Series != nil || got.Room != nil || !got.StartsAt.Equal(at("2026-11-27 07:30")) || got.Date.Format("2006-01-02") != "2026-11-27" {
		t.Fatalf("the one-off reads %+v", got)
	}
	if _, err := domain.ParseSessionID(got.ID.String()); err != nil || got.ID.Occurrence() {
		t.Errorf("the one-off's id is %s", got.ID)
	}
	if audit := w.audited(w.a); len(audit) != 1 || audit[0].Entity != "class_session" || audit[0].EntityID != got.ID.String() {
		t.Errorf("the audit holds %+v", audit)
	}
}

// TestACreateIsRefusedOutsideScopeAndOnAnArchivedClass answers another
// teacher's class, archived or not, as a missing one, and the caller's own
// archived class with ErrClassArchived; nothing is written.
func TestACreateIsRefusedOutsideScopeAndOnAnArchivedClass(t *testing.T) {
	w := newWorld(t)
	theirs, archived := w.class(w.b, nil), w.class(w.a, nil)
	w.exec(`UPDATE app.classes SET archived_at = now() WHERE id = ANY($1::uuid[])`, []string{archived})
	for name, c := range map[string]struct {
		class string
		want  error
	}{
		"another teacher's": {theirs, domain.ErrNotFound},
		"archived":          {archived, domain.ErrClassArchived},
	} {
		_, err := w.app.Commands.CreateSession.Handle(context.Background(), command.CreateSession{Actor: by(w.a), ClassID: c.class, Draft: weekly("2026-11-25", "18:00")})
		if !errors.Is(err, c.want) {
			t.Errorf("%s class: %v, want %v", name, err, c.want)
		}
	}
	w.exec(`UPDATE app.classes SET archived_at = now() WHERE id = $1::uuid`, theirs)
	if _, err := w.app.Commands.CreateSession.Handle(context.Background(), command.CreateSession{Actor: by(w.a), ClassID: theirs, Draft: weekly("2026-11-25", "18:00")}); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("another teacher's archived class: %v, want not found", err)
	}
	if audit := w.audited(w.a); len(audit) != 0 {
		t.Errorf("refused creates wrote %+v", audit)
	}
}

// TestAMovedOccurrenceKeepsItsScheduledDate moves the 25 November occurrence
// to Friday 4 December: its row keeps series_date 25 November, it reads once,
// on the 4th, under its first id, beside the series' own 2 December, and the
// week of the 25th no longer holds it.
func TestAMovedOccurrenceKeepsItsScheduledDate(t *testing.T) {
	w := newWorld(t)
	class := w.class(w.a, nil)
	first := w.create(w.a, class, weekly("2026-11-25", "18:00"))
	moved := w.patch(w.a, first.ID.String(), domain.Patch{Date: ptr(day("2026-12-04")), Note: domain.TextPatch{Set: true, Value: ptr("  Phòng 3 tuần này ")}})
	if moved.ID != first.ID || moved.Date.Format("2006-01-02") != "2026-12-04" || !moved.StartsAt.Equal(at("2026-12-04 18:00")) || !moved.Changed {
		t.Fatalf("the moved occurrence reads %+v", moved)
	}
	if moved.Note == nil || *moved.Note != "Phòng 3 tuần này" {
		t.Errorf("the note reads %v", moved.Note)
	}
	if rows := w.rowsOf(first.Series.ID); len(rows) != 1 || rows["2026-11-25"] != "Phòng 3 tuần này|false" {
		t.Errorf("the series' rows are %v", rows)
	}
	if week := w.week(w.a, "2026-11-23"); len(week) != 0 {
		t.Errorf("the week it moved from holds %+v", week)
	}
	week := w.week(w.a, "2026-11-30")
	if len(week) != 2 || week[0].ID.String() != first.Series.ID+".20261202" || week[1].ID != first.ID || week[1].Date.Format("2006-01-02") != "2026-12-04" {
		t.Errorf("the week it moved to holds %+v", week)
	}
	audit := w.audited(w.a)
	if last := audit[len(audit)-1]; last.Action != "session.updated" || last.Entity != "class_session" || last.Diff["sessionId"] != first.ID.String() {
		t.Errorf("the edit audited %+v", last)
	}
	for _, e := range audit {
		for _, v := range e.Diff {
			if s, ok := v.(string); ok && s == "Phòng 3 tuần này" {
				t.Errorf("%s keeps the note's text", e.Action)
			}
		}
	}
}

// TestCancelAndRestoreAnOccurrence cancels an occurrence that has no row,
// which writes one, then restores it, which leaves nothing for the row to
// hold, so it goes. Each is one audit row, session.cancelled then
// session.updated; a second restore writes nothing.
func TestCancelAndRestoreAnOccurrence(t *testing.T) {
	w := newWorld(t)
	class := w.class(w.a, nil)
	first := w.create(w.a, class, weekly("2026-11-25", "18:00"))
	next := domain.OccurrenceID(first.Series.ID, day("2026-12-02")).String()
	cancelled := w.patch(w.a, next, domain.Patch{Cancelled: ptr(true)})
	if !cancelled.Cancelled || cancelled.Changed || cancelled.RowID == "" {
		t.Fatalf("the cancelled occurrence reads %+v", cancelled)
	}
	if rows := w.rowsOf(first.Series.ID); rows["2026-12-02"] != "|true" {
		t.Errorf("the rows are %v", rows)
	}
	restored := w.patch(w.a, next, domain.Patch{Cancelled: ptr(false)})
	if restored.Cancelled || restored.RowID != "" {
		t.Errorf("the restored occurrence reads %+v", restored)
	}
	if rows := w.rowsOf(first.Series.ID); len(rows) != 0 {
		t.Errorf("a restored occurrence keeps rows %v", rows)
	}
	w.patch(w.a, next, domain.Patch{Cancelled: ptr(false)})
	audit := w.audited(w.a)
	if len(audit) != 3 || audit[1].Action != "session.cancelled" || audit[2].Action != "session.updated" || audit[1].EntityID != cancelled.RowID {
		t.Errorf("the audit holds %+v", audit)
	}
	for _, s := range w.week(w.a, "2026-11-30") {
		if s.Cancelled {
			t.Errorf("the week reads %s cancelled", s.ID)
		}
	}
}

// TestAnOccurrenceTheSeriesDoesNotHaveIsNotFound refuses a date that is not
// the series' weekday, one before its first date, a malformed id, and
// another teacher's occurrence, each as not found.
func TestAnOccurrenceTheSeriesDoesNotHaveIsNotFound(t *testing.T) {
	w := newWorld(t)
	first := w.create(w.a, w.class(w.a, nil), weekly("2026-11-25", "18:00"))
	for name, id := range map[string]string{
		"a Thursday":       first.Series.ID + ".20261126",
		"before the first": first.Series.ID + ".20261118",
		"malformed":        first.Series.ID + ".2026-11-25",
		"a series row":     first.Series.ID,
	} {
		if _, err := w.app.Commands.UpdateSession.Handle(context.Background(), command.UpdateSession{Actor: by(w.a), SessionID: id, Patch: domain.Patch{Cancelled: ptr(true)}}); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := w.app.Commands.UpdateSession.Handle(context.Background(), command.UpdateSession{Actor: by(w.b), SessionID: first.ID.String(), Patch: domain.Patch{Cancelled: ptr(true)}}); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("another teacher's occurrence: %v", err)
	}
	got, err := w.app.Queries.GetSession.Handle(context.Background(), query.GetSession{Scope: by(w.a).Scope, ViewerID: w.a, SessionID: first.ID.String()})
	if err != nil || got.ID != first.ID || !got.StartsAt.Equal(first.StartsAt) {
		t.Errorf("reading the first occurrence back: %+v, %v", got, err)
	}
}

// TestDeleteRemovesOnlyAOneOffWithoutAttendance removes a one-off, refuses an
// occurrence with ErrInSeries and a one-off with attendance with
// ErrHasAttendance, and answers another teacher's one-off as missing.
func TestDeleteRemovesOnlyAOneOffWithoutAttendance(t *testing.T) {
	w := newWorld(t)
	class := w.class(w.a, nil)
	gone, marked := w.create(w.a, class, once("2026-11-20", "18:00")), w.create(w.a, class, once("2026-11-21", "18:00"))
	w.mark(marked.RowID, w.student(), w.a)
	occurrence := w.create(w.a, class, weekly("2026-11-25", "18:00"))
	del := func(who, id string) error {
		_, err := w.app.Commands.DeleteSession.Handle(context.Background(), command.DeleteSession{Actor: by(who), SessionID: id})
		return err
	}
	if err := del(w.b, gone.ID.String()); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("another teacher's one-off: %v", err)
	}
	if err := del(w.b, occurrence.ID.String()); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("another teacher's occurrence: %v", err)
	}
	if err := del(w.a, occurrence.ID.String()); !errors.Is(err, domain.ErrInSeries) {
		t.Errorf("an occurrence: %v", err)
	}
	if err := del(w.a, marked.ID.String()); !errors.Is(err, domain.ErrHasAttendance) {
		t.Errorf("a one-off with attendance: %v", err)
	}
	if err := del(w.a, gone.ID.String()); err != nil {
		t.Fatal(err)
	}
	if n := w.id(`SELECT count(*)::text FROM app.class_sessions WHERE id = ANY($1::uuid[])`, []string{gone.RowID, marked.RowID}); n != "1" {
		t.Errorf("%s of the two one-offs remain, want the marked one", n)
	}
	audit := w.audited(w.a)
	if last := audit[len(audit)-1]; last.Action != "session.deleted" || last.EntityID != gone.RowID {
		t.Errorf("the delete audited %+v", last)
	}
}

// TestASplitWithAttendanceTodayStartsTomorrow changes a Monday series on
// Monday 23 November, whose occurrence today has attendance. The change
// starts tomorrow: the old series ends today, the new one runs from tomorrow
// to the old last date, the per-date change of 30 November is dropped and
// counted, and the 7 December row, which has attendance, stays with the old
// series. Today's occurrence keeps its time.
func TestASplitWithAttendanceTodayStartsTomorrow(t *testing.T) {
	w := newWorld(t)
	class := w.class(w.a, nil)
	d := weekly("2026-11-02", "18:00")
	d.EndsOn = ptr(day("2027-01-11"))
	old := w.create(w.a, class, d).Series
	today := w.patch(w.a, domain.OccurrenceID(old.ID, day("2026-11-23")).String(), domain.Patch{Note: domain.TextPatch{Set: true, Value: ptr("Kiểm tra")}})
	w.mark(today.RowID, w.student(), w.a)
	w.patch(w.a, domain.OccurrenceID(old.ID, day("2026-11-30")).String(), domain.Patch{Note: domain.TextPatch{Set: true, Value: ptr("Phòng 3")}})
	kept := w.patch(w.a, domain.OccurrenceID(old.ID, day("2026-12-07")).String(), domain.Patch{Date: ptr(day("2026-11-20"))})
	w.mark(kept.RowID, w.student(), w.a)

	later := domain.Clock{Hour: 19, Minute: 30}
	edit, err := w.app.Commands.UpdateSeries.Handle(context.Background(), command.UpdateSeries{
		Actor: by(w.a), SeriesID: old.ID, FromDate: day("2026-11-23"), Change: domain.SeriesChange{Start: &later},
	})
	if err != nil {
		t.Fatal(err)
	}
	if edit.Dropped != 1 || edit.Series.ID == old.ID || !edit.Series.StartsOn.Equal(day("2026-11-24")) || !edit.Series.EndsOn.Equal(day("2027-01-11")) || edit.Series.Start != later {
		t.Fatalf("the split is %+v", edit)
	}
	if ends := w.id(`SELECT to_char(ends_on, 'YYYY-MM-DD') FROM app.class_session_series WHERE id = $1::uuid`, old.ID); ends != "2026-11-23" {
		t.Errorf("the old series ends %s", ends)
	}
	if rows := w.rowsOf(old.ID); len(rows) != 2 || rows["2026-11-23"] == "" || rows["2026-12-07"] == "" {
		t.Errorf("the old series keeps rows %v", rows)
	}
	week := w.week(w.a, "2026-11-23")
	if len(week) != 1 || week[0].ID.String() != old.ID+".20261123" || !week[0].StartsAt.Equal(at("2026-11-23 18:00")) || !week[0].HasAttendance {
		t.Fatalf("this week holds %+v", week)
	}
	next := w.week(w.a, "2026-11-30")
	if len(next) != 1 || next[0].ID.String() != edit.Series.ID+".20261130" || !next[0].StartsAt.Equal(at("2026-11-30 19:30")) || next[0].Note != nil {
		t.Errorf("next week holds %+v", next)
	}
	audit := w.audited(w.a)
	last := audit[len(audit)-1]
	if last.Action != "session_series.updated" || last.EntityID != old.ID || last.Diff["newSeriesId"] != edit.Series.ID || last.Diff["droppedChanges"] != float64(1) || last.Diff["fromDate"] != "2026-11-24" {
		t.Errorf("the split audited %+v", last)
	}
}

// TestASplitBeforeTodayIsRefused names fromDate for yesterday in the
// series' zone, and changes nothing.
func TestASplitBeforeTodayIsRefused(t *testing.T) {
	w := newWorld(t)
	old := w.create(w.a, w.class(w.a, nil), weekly("2026-11-02", "18:00")).Series
	longer := 120
	_, err := w.app.Commands.UpdateSeries.Handle(context.Background(), command.UpdateSeries{
		Actor: by(w.a), SeriesID: old.ID, FromDate: day("2026-11-22"), Change: domain.SeriesChange{DurationMinutes: &longer},
	})
	var refused *validation.Error
	if !errors.As(err, &refused) || refused.Fields[0].Field != "fromDate" {
		t.Errorf("yesterday: %v", err)
	}
	if _, err := w.app.Commands.UpdateSeries.Handle(context.Background(), command.UpdateSeries{
		Actor: by(w.b), SeriesID: old.ID, FromDate: day("2026-11-30"), Change: domain.SeriesChange{DurationMinutes: &longer},
	}); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("another teacher's series: %v", err)
	}
}

// TestEndingBeforeTheStartRemovesTheSeries ends a series that has not begun
// on a day before its first date: it is removed, with its per-date rows, and
// one session_series.ended row says so.
func TestEndingBeforeTheStartRemovesTheSeries(t *testing.T) {
	w := newWorld(t)
	s := w.create(w.a, w.class(w.a, nil), weekly("2026-12-07", "18:00")).Series
	w.patch(w.a, domain.OccurrenceID(s.ID, day("2026-12-14")).String(), domain.Patch{Cancelled: ptr(true)})
	ended, err := w.app.Commands.EndSeries.Handle(context.Background(), command.EndSeries{Actor: by(w.a), SeriesID: s.ID, LastDate: day("2026-12-01")})
	if err != nil || ended != nil {
		t.Fatalf("ending before the start: %+v, %v", ended, err)
	}
	if n := w.id(`SELECT (SELECT count(*) FROM app.class_session_series WHERE id = $1::uuid) + (SELECT count(*) FROM app.class_sessions WHERE series_id = $1::uuid)`, s.ID); n != "0" {
		t.Errorf("%s series and rows remain", n)
	}
	audit := w.audited(w.a)
	if last := audit[len(audit)-1]; last.Action != "session_series.ended" || last.EntityID != s.ID || last.Diff["removed"] != true || last.Diff["droppedChanges"] != float64(1) {
		t.Errorf("the end audited %+v", last)
	}
}

// TestEndingKeepsAttendanceAttached refuses an end that would cut an
// occurrence with attendance off its series, and changes nothing; an end
// after it sets the last date and drops the later per-date rows.
func TestEndingKeepsAttendanceAttached(t *testing.T) {
	w := newWorld(t)
	s := w.create(w.a, w.class(w.a, nil), weekly("2026-11-02", "18:00")).Series
	marked := w.patch(w.a, domain.OccurrenceID(s.ID, day("2026-11-16")).String(), domain.Patch{Note: domain.TextPatch{Set: true, Value: ptr("Ôn tập")}})
	w.mark(marked.RowID, w.student(), w.a)
	w.patch(w.a, domain.OccurrenceID(s.ID, day("2026-12-07")).String(), domain.Patch{Cancelled: ptr(true)})
	end := func(last string) (*domain.Series, error) {
		return w.app.Commands.EndSeries.Handle(context.Background(), command.EndSeries{Actor: by(w.a), SeriesID: s.ID, LastDate: day(last)})
	}
	if _, err := end("2026-11-09"); !errors.Is(err, domain.ErrHasAttendance) {
		t.Errorf("ending before the marked date: %v", err)
	}
	if rows := w.rowsOf(s.ID); len(rows) != 2 {
		t.Errorf("a refused end left rows %v", rows)
	}
	ended, err := end("2026-11-30")
	if err != nil || ended == nil || !ended.EndsOn.Equal(day("2026-11-30")) {
		t.Fatalf("ending after it: %+v, %v", ended, err)
	}
	if rows := w.rowsOf(s.ID); len(rows) != 1 || rows["2026-11-16"] == "" {
		t.Errorf("the ended series keeps rows %v", rows)
	}
	if _, err := w.app.Commands.EndSeries.Handle(context.Background(), command.EndSeries{Actor: by(w.b), SeriesID: s.ID, LastDate: day("2026-11-30")}); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("another teacher's series: %v", err)
	}
}

// TestFortyTwoDaysOfFiftySeriesTakeTwoQueries reads six weeks of fifty
// weekly series across ten classes, with per-date rows in some, in two
// queries: one for the series and one for the rows, not one per series.
func TestFortyTwoDaysOfFiftySeriesTakeTwoQueries(t *testing.T) {
	w := newWorld(t)
	var classes []string
	for range 10 {
		classes = append(classes, w.class(w.a, nil))
	}
	var first []domain.Session
	for i := range 50 {
		d := weekly("2026-11-23", "07:00")
		d.Date = d.Date.AddDate(0, 0, i%7)
		d.Start.Hour += i / 7
		first = append(first, w.create(w.a, classes[i%10], d))
	}
	for _, s := range first[:5] {
		w.patch(w.a, s.ID.String(), domain.Patch{Cancelled: ptr(true)})
	}
	from := at("2026-11-23 00:00")
	before := w.queries.count()
	got, err := w.app.Queries.Occurrences.Handle(context.Background(), query.Occurrences{
		Scope: by(w.a).Scope, From: from, To: from.AddDate(0, 0, 42),
	})
	if err != nil {
		t.Fatal(err)
	}
	if n := w.queries.count() - before; n != 2 {
		t.Errorf("forty-two days of fifty series took %d queries, want 2", n)
	}
	cancelled := 0
	for _, s := range got {
		if s.Cancelled {
			cancelled++
		}
	}
	if len(got) != 300 || cancelled != 5 {
		t.Errorf("the range holds %d sessions, %d cancelled, want 300 and 5", len(got), cancelled)
	}
	mine, err := w.app.Queries.Occurrences.Handle(context.Background(), query.Occurrences{
		Scope: by(w.b).Scope, From: from, To: from.AddDate(0, 0, 42),
	})
	if err != nil || len(mine) != 0 {
		t.Errorf("another teacher's range holds %d sessions (%v)", len(mine), err)
	}
	one, err := w.app.Queries.Occurrences.Handle(context.Background(), query.Occurrences{
		Scope: by(w.a).Scope, ClassIDs: classes[:1], From: from, To: from.AddDate(0, 0, 42),
	})
	if err != nil || len(one) != 30 {
		t.Errorf("one class's range holds %d sessions (%v), want 30", len(one), err)
	}
}
