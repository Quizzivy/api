//go:build integration

package application_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	organizationmodel "quizzivy/internal/modules/organization/application/model"
	"quizzivy/internal/modules/timetable/application/command"
	"quizzivy/internal/modules/timetable/application/query"
	"quizzivy/internal/modules/timetable/domain"
	"quizzivy/internal/shared/access"
	"quizzivy/internal/shared/actor"
)

// term is the organization's CurrentTerm over one term: it holds the calendar
// day of on, read in on's own location, as adapters.Terms does.
type term struct {
	starts, ends time.Time
}

func (t term) CurrentTerm(_ context.Context, on time.Time) (*organizationmodel.Term, error) {
	today := domain.Day(on)
	if today.Before(t.starts) || today.After(t.ends) {
		return nil, nil
	}
	return &organizationmodel.Term{ID: "019535d9-3df7-79fb-b466-fa907fa17f9e", Name: "Học kỳ 1", StartsOn: t.starts, EndsOn: t.ends}, nil
}

type photos struct{}

func (photos) AvatarURL(_ context.Context, key string) (string, error) {
	return "https://photos.example/" + key, nil
}

func (w *world) member(class, student string, joined time.Time) {
	w.t.Helper()
	w.exec(`INSERT INTO app.class_members (class_id, user_id, joined_via, joined_at) VALUES ($1::uuid, $2::uuid, 'admin', $3)`, class, student, joined)
}

func set(student string, s domain.Status) domain.MarkInput {
	return domain.MarkInput{StudentID: student, Status: &s}
}

func noted(student string, s domain.Status, note *string) domain.MarkInput {
	m := set(student, s)
	m.Note = domain.TextPatch{Set: true, Value: note}
	return m
}

func unset(student string) domain.MarkInput { return domain.MarkInput{StudentID: student} }

func (w *world) trySave(who actor.Actor, session string, marks ...domain.MarkInput) (domain.Register, error) {
	return w.app.Commands.SaveAttendance.Handle(context.Background(), command.SaveAttendance{Actor: who, SessionID: session, Marks: marks})
}

func (w *world) save(who string, session string, marks ...domain.MarkInput) domain.Register {
	w.t.Helper()
	r, err := w.trySave(by(who), session, marks...)
	if err != nil {
		w.t.Fatalf("saving the register of %s: %v", session, err)
	}
	return r
}

func (w *world) register(who actor.Actor, session string) domain.Register {
	w.t.Helper()
	r, err := w.app.Queries.SessionAttendance.Handle(context.Background(), query.SessionAttendance{Scope: who.Scope, ViewerID: who.ID, SessionID: session})
	if err != nil {
		w.t.Fatalf("reading the register of %s: %v", session, err)
	}
	return r
}

func occurrence(series *domain.Series, date string) string {
	return domain.OccurrenceID(series.ID, day(date)).String()
}

func rates(r domain.Register) map[string]any {
	out := map[string]any{}
	for _, s := range r.Students {
		out[s.StudentID] = deref(s.Tally.Rate())
	}
	return out
}

func deref(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}

func (w *world) rowCount(series string) int {
	w.t.Helper()
	var n int
	if err := w.tx.QueryRow(context.Background(), `SELECT count(*) FROM app.class_sessions WHERE series_id = $1::uuid`, series).Scan(&n); err != nil {
		w.t.Fatal(err)
	}
	return n
}

// TestTheRateCountsLateAndLeavesOutUnmarkedAndCancelledSessions marks four
// Mondays of a series (the fourth this morning). The first student is present,
// late, absent on a session that is then cancelled, and present: 3 of 3. The
// second is absent, absent, unmarked on the cancelled one and late: 1 of 3.
// The third is never marked: no rate. No term holds today, so the rates are
// overall, and the register lists the members latest joined first.
func TestTheRateCountsLateAndLeavesOutUnmarkedAndCancelledSessions(t *testing.T) {
	w := newWorld(t)
	class := w.class(w.a, nil)
	first, second, third := w.student(), w.student(), w.student()
	w.member(class, first, at("2026-09-01 08:00"))
	w.member(class, second, at("2026-09-03 08:00"))
	w.member(class, third, at("2026-09-02 08:00"))
	series := w.create(w.a, class, weekly("2026-11-02", "08:00")).Series

	w.save(w.a, occurrence(series, "2026-11-02"), set(first, domain.Present), set(second, domain.Absent))
	w.save(w.a, occurrence(series, "2026-11-09"), set(first, domain.Late), set(second, domain.Absent))
	w.save(w.a, occurrence(series, "2026-11-16"), set(first, domain.Absent))
	w.patch(w.a, occurrence(series, "2026-11-16"), domain.Patch{Cancelled: ptr(true)})
	got := w.save(w.a, occurrence(series, "2026-11-23"), set(first, domain.Present), set(second, domain.Late))

	if want := map[string]any{first: 100, second: 33, third: nil}; !mapsEqual(rates(got), want) {
		t.Errorf("the rates are %v, want %v", rates(got), want)
	}
	if order := []string{got.Students[0].StudentID, got.Students[1].StudentID, got.Students[2].StudentID}; !slices.Equal(order, []string{second, third, first}) {
		t.Errorf("the roster reads %v, want the latest joined first", order)
	}
	if got.Term != nil || got.Counts() != (domain.RosterCounts{Present: 1, Late: 1, Unmarked: 1}) {
		t.Errorf("the register's term is %v and counts %+v", got.Term, got.Counts())
	}
	if !got.Session.HasAttendance || got.Session.ID.String() != occurrence(series, "2026-11-23") || got.Session.RowID == "" {
		t.Errorf("the register's session reads %+v", got.Session)
	}
	if again := w.register(by(w.a), occurrence(series, "2026-11-23")); !mapsEqual(rates(again), rates(got)) || again.Counts() != got.Counts() {
		t.Errorf("reading the register again gives %v and %+v", rates(again), again.Counts())
	}
}

func mapsEqual(a, b map[string]any) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if bv, ok := b[k]; !ok || bv != v {
			return false
		}
	}
	return true
}

// TestTheRateCoversTheTermThatHoldsTodayInTheTeachersZone has a term from 24
// November and a teacher in Tokyo at 01:30 on the 24th, when it is still the
// 23rd in Ho Chi Minh City. The student was absent on the 20th and present at
// 00:30 Tokyo on the 24th, so the teacher reads 100 over the term; the Admin,
// in Ho Chi Minh City, where no term holds today, reads 50 overall.
func TestTheRateCoversTheTermThatHoldsTodayInTheTeachersZone(t *testing.T) {
	w := newWorld(t)
	w.app.WithTerms(term{starts: day("2026-11-24"), ends: day("2027-01-31")})
	w.zones[w.a] = "Asia/Tokyo"
	w.now = at("2026-11-23 23:30")
	class := w.class(w.a, nil)
	student := w.student()
	w.member(class, student, at("2026-09-01 08:00"))
	before := w.create(w.a, class, once("2026-11-20", "10:00"))
	inTerm := w.create(w.a, class, once("2026-11-24", "00:30"))
	w.save(w.a, before.ID.String(), set(student, domain.Absent))
	got := w.save(w.a, inTerm.ID.String(), set(student, domain.Present))
	if got.Term == nil || got.Term.Name != "Học kỳ 1" || rates(got)[student] != 100 {
		t.Errorf("the teacher in Tokyo reads term %v and rates %v, want the term and 100", got.Term, rates(got))
	}
	admin := actor.Actor{ID: w.b, Scope: access.Scope{UserID: w.b, All: true}}
	overall := w.register(admin, inTerm.ID.String())
	if overall.Term != nil || rates(overall)[student] != 50 {
		t.Errorf("the Admin in Ho Chi Minh City reads term %v and rates %v, want none and 50", overall.Term, rates(overall))
	}
}

// TestASaveIsRefusedBeforeTheSessionStartsOnACancelledOneAndOffTheRoster
// refuses next week's occurrence and one that starts in 16 minutes, takes one
// that starts in 15, refuses a cancelled one, and refuses a student of
// another class and an unknown id beside a member, naming only those two;
// nothing is written, and no occurrence row is made. Another teacher's
// session answers as a missing one does. A student who left the class keeps
// the mark taken while they were in it, and the teacher can change it.
func TestASaveIsRefusedBeforeTheSessionStartsOnACancelledOneAndOffTheRoster(t *testing.T) {
	w := newWorld(t)
	class, other := w.class(w.a, nil), w.class(w.a, nil)
	member, stranger, ghost := w.student(), w.student(), uuid.NewString()
	w.member(class, member, at("2026-09-01 08:00"))
	w.member(other, stranger, at("2026-09-01 08:00"))
	series := w.create(w.a, class, weekly("2026-11-02", "08:00")).Series
	soon := w.create(w.a, class, once("2026-11-23", "10:15"))
	late := w.create(w.a, class, once("2026-11-23", "10:16"))

	for name, c := range map[string]struct {
		session string
		want    error
	}{
		"next week's":            {occurrence(series, "2026-11-30"), domain.ErrNotStarted},
		"one starting in 16 min": {late.ID.String(), domain.ErrNotStarted},
	} {
		if _, err := w.trySave(by(w.a), c.session, set(member, domain.Present)); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", name, err, c.want)
		}
	}
	w.save(w.a, soon.ID.String(), set(member, domain.Present))

	w.patch(w.a, occurrence(series, "2026-11-16"), domain.Patch{Cancelled: ptr(true)})
	if _, err := w.trySave(by(w.a), occurrence(series, "2026-11-16"), set(member, domain.Present)); !errors.Is(err, domain.ErrCancelled) {
		t.Errorf("a cancelled occurrence: %v, want cancelled", err)
	}

	rows := w.rowCount(series.ID)
	_, err := w.trySave(by(w.a), occurrence(series, "2026-11-09"), set(member, domain.Present), set(stranger, domain.Present), set(ghost, domain.Late))
	var refused *domain.RefusedMarks
	if !errors.As(err, &refused) || refused.Reason != domain.RefusedOffRoster || !slices.Equal(refused.StudentIDs, []string{stranger, ghost}) {
		t.Fatalf("students off the roster: %v", err)
	}
	if after := w.rowCount(series.ID); after != rows {
		t.Errorf("a refused save made %d occurrence rows", after-rows)
	}
	if _, err := w.trySave(by(w.b), occurrence(series, "2026-11-09"), set(member, domain.Present)); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("another teacher's occurrence: %v, want not found", err)
	}
	if _, err := w.trySave(by(w.b), soon.ID.String(), set(member, domain.Present)); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("another teacher's one-off: %v, want not found", err)
	}

	w.exec(`DELETE FROM app.class_members WHERE class_id = $1::uuid AND user_id = $2::uuid`, class, member)
	got := w.save(w.a, soon.ID.String(), set(member, domain.Late))
	if len(got.Students) != 1 || got.Students[0].StudentID != member || *got.Students[0].Status != domain.Late {
		t.Errorf("a former member's mark reads %+v", got.Students)
	}
	if _, err := w.trySave(by(w.a), occurrence(series, "2026-11-09"), set(member, domain.Present)); !errors.As(err, &refused) {
		t.Errorf("a former member with no mark on the session: %v, want refused", err)
	}
	for _, e := range w.audited(w.a) {
		if e.Action == "attendance.saved" && e.Diff["sessionId"] == occurrence(series, "2026-11-09") {
			t.Errorf("a refused save was audited: %+v", e)
		}
	}
}

// TestANoteIsKeptClearedAndNeverAudited saves a note, keeps it through a save
// that leaves it out, changes it, clears it with a null, and refuses one over
// 500 characters and one beside a null status. The audit says only that the
// note changed.
func TestANoteIsKeptClearedAndNeverAudited(t *testing.T) {
	w := newWorld(t)
	class := w.class(w.a, nil)
	student, other := w.student(), w.student()
	w.member(class, student, at("2026-09-01 08:00"))
	w.member(class, other, at("2026-09-02 08:00"))
	session := w.create(w.a, class, once("2026-11-23", "08:00")).ID.String()
	note := func(r domain.Register) any {
		for _, s := range r.Students {
			if s.StudentID == student {
				if s.Note == nil {
					return nil
				}
				return *s.Note
			}
		}
		return "absent from the roster"
	}

	if got := note(w.save(w.a, session, noted(student, domain.Late, ptr("  Kẹt xe  ")))); got != "Kẹt xe" {
		t.Errorf("a saved note reads %v", got)
	}
	if got := note(w.save(w.a, session, set(student, domain.Present))); got != "Kẹt xe" {
		t.Errorf("a save that leaves the note out reads %v", got)
	}
	if got := note(w.save(w.a, session, noted(student, domain.Present, ptr("Mưa to")))); got != "Mưa to" {
		t.Errorf("a changed note reads %v", got)
	}
	if got := note(w.save(w.a, session, noted(student, domain.Present, nil))); got != nil {
		t.Errorf("a cleared note reads %v", got)
	}
	var refused *domain.RefusedMarks
	if _, err := w.trySave(by(w.a), session, noted(student, domain.Present, ptr(strings.Repeat("a", 501)))); !errors.As(err, &refused) ||
		refused.Reason != domain.RefusedNoteTooLong || !slices.Equal(refused.StudentIDs, []string{student}) {
		t.Errorf("a note of 501 characters: %v", err)
	}
	if _, err := w.trySave(by(w.a), session, set(other, domain.Present), domain.MarkInput{StudentID: student, Note: domain.TextPatch{Set: true, Value: ptr("Ốm")}}); !errors.As(err, &refused) ||
		refused.Reason != domain.RefusedNoteWithoutStatus || !slices.Equal(refused.StudentIDs, []string{student}) {
		t.Errorf("a note beside a null status: %v", err)
	}
	w.save(w.a, session, noted(student, domain.Absent, ptr("Về quê")))
	if got := w.save(w.a, session, unset(student)); note(got) != nil || got.Counts().Unmarked != 2 {
		t.Errorf("unmarking reads note %v and counts %+v", note(got), got.Counts())
	}
	if n := w.count(`SELECT count(*) FROM app.attendance_marks m JOIN app.class_sessions s ON s.id = m.session_id WHERE s.class_id = $1::uuid`, class); n != 0 {
		t.Errorf("unmarking left %d marks", n)
	}

	for _, e := range w.audited(w.a) {
		raw, _ := json.Marshal(e.Diff)
		for _, text := range []string{"Kẹt xe", "Mưa to", "Về quê", "Ốm"} {
			if strings.Contains(string(raw), text) {
				t.Errorf("the audit row %s holds the note %q: %s", e.Action, text, raw)
			}
		}
	}
}

func (w *world) count(sql string, args ...any) int {
	w.t.Helper()
	var n int
	if err := w.tx.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		w.t.Fatalf("%s: %v", sql, err)
	}
	return n
}

// TestEachSaveAuditsTheChangedStatusesAndNotes marks, changes, unmarks and
// re-notes four students in one save: one attendance.saved row on the
// session's row lists each change, from and to, and "note changed"; a save
// that changes nothing writes none.
func TestEachSaveAuditsTheChangedStatusesAndNotes(t *testing.T) {
	w := newWorld(t)
	class := w.class(w.a, nil)
	students := []string{w.student(), w.student(), w.student(), w.student(), w.student()}
	for i, s := range students {
		w.member(class, s, at("2026-09-01 08:00").Add(time.Duration(i)*time.Hour))
	}
	series := w.create(w.a, class, weekly("2026-11-02", "08:00")).Series
	session := occurrence(series, "2026-11-23")
	w.save(w.a, session, set(students[1], domain.Present), noted(students[2], domain.Present, ptr("Ốm")), noted(students[3], domain.Absent, ptr("Ốm")), set(students[4], domain.Late))
	saved := w.audited(w.a)
	got := w.save(w.a, session,
		set(students[0], domain.Present),                // unmarked → present
		set(students[1], domain.Late),                   // present → late
		unset(students[2]),                              // present → unmarked, with its note
		noted(students[3], domain.Absent, ptr("Đã về")), // the note alone
		set(students[4], domain.Late),                   // unchanged
	)
	audit := w.audited(w.a)
	if len(audit) != len(saved)+1 {
		t.Fatalf("one save wrote %d audit rows", len(audit)-len(saved))
	}
	last := audit[len(audit)-1]
	if last.Action != "attendance.saved" || last.Entity != "class_session" || last.EntityID != got.Session.RowID || last.Diff["sessionId"] != session || last.Diff["classId"] != class {
		t.Fatalf("the save audited %+v", last)
	}
	want := map[string]map[string]any{
		students[0]: {"studentId": students[0], "from": nil, "to": "present"},
		students[1]: {"studentId": students[1], "from": "present", "to": "late"},
		students[2]: {"studentId": students[2], "from": "present", "to": nil, "note": "changed"},
		students[3]: {"studentId": students[3], "note": "changed"},
	}
	changes, _ := last.Diff["changes"].([]any)
	if len(changes) != len(want) {
		t.Fatalf("the diff lists %d changes, want %d: %v", len(changes), len(want), last.Diff)
	}
	for _, c := range changes {
		change := c.(map[string]any)
		expected := want[change["studentId"].(string)]
		if len(change) != len(expected) {
			t.Errorf("the change %v, want %v", change, expected)
			continue
		}
		for k, v := range expected {
			if change[k] != v {
				t.Errorf("the change %v, want %v", change, expected)
			}
		}
	}
	w.save(w.a, session, set(students[4], domain.Late), unset(students[0]))
	if again := w.audited(w.a); len(again) != len(audit)+1 {
		t.Errorf("a save that unmarks one student wrote %d rows", len(again)-len(audit))
	}
	before := len(w.audited(w.a))
	w.save(w.a, session, set(students[4], domain.Late), unset(students[0]))
	if after := len(w.audited(w.a)); after != before {
		t.Errorf("a save that changes nothing wrote %d audit rows", after-before)
	}
	nothing := occurrence(series, "2026-11-16")
	w.save(w.a, nothing, unset(students[0]))
	if rows := w.rowsOf(series.ID); rows["2026-11-16"] != "" {
		t.Errorf("unmarking on an occurrence with no row made one: %v", rows)
	}
}

// TestTheRegisterListBoundsCountsAndPagesTheStartedSessions lists a class's
// started, uncancelled sessions, its rows and the occurrences that have none,
// newest first; bounds them by since and before, keeps the marked ones or the
// others, counts marks by status and the students each register lists (a
// marked student who left included), and counts notMarked over the whole
// scope, agreeing with a full walk of marked=false. Five queries, whatever the
// number of series; another teacher's class is not found; a session that
// starts now has started.
func TestTheRegisterListBoundsCountsAndPagesTheStartedSessions(t *testing.T) {
	w := newWorld(t)
	class, quiet := w.class(w.a, ptr("P.1")), w.class(w.a, nil)
	stays, leaves, extra := w.student(), w.student(), w.student()
	w.member(class, stays, at("2026-09-01 08:00"))
	w.member(class, leaves, at("2026-09-01 09:00"))
	w.member(class, extra, at("2026-09-01 10:00"))
	w.exec(`UPDATE app.users SET disabled_at = now() WHERE id = $1::uuid`, extra)
	monday := w.create(w.a, class, weekly("2026-11-02", "08:00")).Series   // 2, 9, 16, 23 Nov started
	thursday := w.create(w.a, class, weekly("2026-11-05", "08:00")).Series // 5, 12, 19 Nov started
	w.create(w.a, quiet, weekly("2026-11-17", "18:00"))                    // 17 Nov started
	oneOff := w.create(w.a, class, once("2026-11-20", "08:00"))
	w.create(w.a, class, once("2026-11-23", "10:30")) // later today: not started
	w.patch(w.a, occurrence(thursday, "2026-11-12"), domain.Patch{Cancelled: ptr(true)})
	w.patch(w.a, occurrence(monday, "2026-11-09"), domain.Patch{Note: domain.TextPatch{Set: true, Value: ptr("Phòng 2")}})
	w.save(w.a, occurrence(monday, "2026-11-16"), set(stays, domain.Present), set(leaves, domain.Late))
	w.save(w.a, oneOff.ID.String(), set(stays, domain.Absent))
	w.exec(`DELETE FROM app.class_members WHERE class_id = $1::uuid AND user_id = $2::uuid`, class, leaves)

	list := func(who actor.Actor, classID *string, f domain.SessionFilter) (domain.SessionPage, error) {
		return w.app.Queries.AttendanceSessions.Handle(context.Background(), query.AttendanceSessions{Scope: who.Scope, ClassID: classID, Filter: f})
	}
	before := w.queries.count()
	all, err := list(by(w.a), nil, domain.SessionFilter{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if n := w.queries.count() - before; n != 4 {
		t.Errorf("the list took %d queries, want 4 without a classId", n)
	}
	want := []string{
		occurrence(monday, "2026-11-23"), oneOff.ID.String(), occurrence(thursday, "2026-11-19"), occurrence(w.seriesOf(quiet), "2026-11-17"),
		occurrence(monday, "2026-11-16"), occurrence(monday, "2026-11-09"), occurrence(thursday, "2026-11-05"), occurrence(monday, "2026-11-02"),
	}
	if got := listedIDs(all); !slices.Equal(got, want) || all.NotMarked != 6 || all.NextBefore != nil {
		t.Fatalf("the list holds %v (notMarked %d, next %v), want %v and 6", got, all.NotMarked, all.NextBefore, want)
	}
	for _, item := range all.Items {
		switch item.ID.String() {
		case occurrence(monday, "2026-11-16"):
			if !item.HasAttendance || item.StudentCount != 2 || item.Counts == nil || *item.Counts != (domain.Counts{Present: 1, Late: 1}) || item.Room == nil || *item.Room != "P.1" {
				t.Errorf("the marked occurrence lists %+v %+v", item.SessionFacts, item.Counts)
			}
		case oneOff.ID.String():
			if !item.HasAttendance || item.StudentCount != 1 || *item.Counts != (domain.Counts{Absent: 1}) {
				t.Errorf("the marked one-off lists %+v %+v", item.SessionFacts, item.Counts)
			}
		case occurrence(w.seriesOf(quiet), "2026-11-17"):
			if item.HasAttendance || item.StudentCount != 0 || item.Counts != nil {
				t.Errorf("the quiet class's occurrence lists %+v", item.SessionFacts)
			}
		default:
			if item.HasAttendance || item.StudentCount != 1 || item.Counts != nil {
				t.Errorf("%s lists %+v", item.ID, item.SessionFacts)
			}
		}
	}

	since, until := at("2026-11-05 08:00"), at("2026-11-19 08:00")
	bounded, err := list(by(w.a), nil, domain.SessionFilter{Since: &since, Before: &until})
	if err != nil || !slices.Equal(listedIDs(bounded), want[3:7]) || bounded.NotMarked != 6 {
		t.Errorf("since %s and before %s list %v (notMarked %d, %v)", since, until, listedIDs(bounded), bounded.NotMarked, err)
	}
	yes, no := true, false
	marked, err := list(by(w.a), nil, domain.SessionFilter{Marked: &yes})
	if err != nil || !slices.Equal(listedIDs(marked), []string{want[1], want[4]}) {
		t.Errorf("marked lists %v (%v)", listedIDs(marked), err)
	}
	walked, cursor := 0, (*time.Time)(nil)
	for {
		page, err := list(by(w.a), nil, domain.SessionFilter{Marked: &no, Limit: 2, Before: cursor})
		if err != nil {
			t.Fatal(err)
		}
		walked += len(page.Items)
		if page.NotMarked != 6 {
			t.Errorf("a page of unmarked sessions counts notMarked %d", page.NotMarked)
		}
		if cursor = page.NextBefore; cursor == nil {
			break
		}
	}
	if walked != all.NotMarked {
		t.Errorf("walking marked=false pages lists %d sessions where notMarked counts %d", walked, all.NotMarked)
	}

	before = w.queries.count()
	one, err := list(by(w.a), &quiet, domain.SessionFilter{Limit: 1})
	if err != nil || len(one.Items) != 1 || one.NotMarked != 1 || one.NextBefore != nil {
		t.Errorf("the quiet class lists %v (notMarked %d, %v)", listedIDs(one), one.NotMarked, err)
	}
	if n := w.queries.count() - before; n != 5 {
		t.Errorf("one class's list took %d queries, want 5", n)
	}
	if _, err := list(by(w.b), &class, domain.SessionFilter{}); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("another teacher's class: %v, want not found", err)
	}
	ghost := uuid.NewString()
	if _, err := list(by(w.a), &ghost, domain.SessionFilter{}); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("an unknown class: %v, want not found", err)
	}
	if theirs, err := list(by(w.b), nil, domain.SessionFilter{}); err != nil || len(theirs.Items) != 0 || theirs.NotMarked != 0 {
		t.Errorf("another teacher's list holds %v and notMarked %d (%v)", listedIDs(theirs), theirs.NotMarked, err)
	}
	edge := w.class(w.a, nil)
	w.create(w.a, edge, once("2026-11-23", "10:00"))
	if started, err := list(by(w.a), &edge, domain.SessionFilter{}); err != nil || len(started.Items) != 1 || started.NotMarked != 1 {
		t.Errorf("a session that starts now lists %v (notMarked %d, %v), want it: it has started", listedIDs(started), started.NotMarked, err)
	}
}

func (w *world) seriesOf(class string) *domain.Series {
	w.t.Helper()
	id := w.id(`SELECT id::text FROM app.class_session_series WHERE class_id = $1::uuid`, class)
	return &domain.Series{ID: id}
}

func listedIDs(p domain.SessionPage) []string {
	out := make([]string, len(p.Items))
	for i, s := range p.Items {
		out[i] = s.ID.String()
	}
	return out
}

// TestTheRosterIsTheActiveMembersAndTheMarked lists the active members and
// a student marked while a member who has since left, not a disabled member
// with no mark, and signs each photo.
func TestTheRosterIsTheActiveMembersAndTheMarked(t *testing.T) {
	w := newWorld(t)
	w.app.WithAvatars(photos{})
	class := w.class(w.a, nil)
	active, left, disabled := w.student(), w.student(), w.student()
	w.member(class, active, at("2026-09-01 08:00"))
	w.member(class, left, at("2026-09-02 08:00"))
	w.member(class, disabled, at("2026-09-03 08:00"))
	w.exec(`UPDATE app.users SET avatar_key = 'avatars/' || id::text WHERE id = $1::uuid`, active)
	session := w.create(w.a, class, once("2026-11-23", "08:00")).ID.String()
	w.save(w.a, session, set(left, domain.Present))
	w.exec(`DELETE FROM app.class_members WHERE user_id = $1::uuid`, left)
	w.exec(`UPDATE app.users SET disabled_at = now() WHERE id = $1::uuid`, disabled)
	got := w.register(by(w.a), session)
	if len(got.Students) != 2 || got.Students[0].StudentID != active || got.Students[1].StudentID != left {
		t.Fatalf("the roster is %+v", got.Students)
	}
	if url := got.Students[0].AvatarURL; url == nil || *url != "https://photos.example/avatars/"+active || got.Students[1].AvatarURL != nil {
		t.Errorf("the photos are %v and %v", got.Students[0].AvatarURL, got.Students[1].AvatarURL)
	}
	if got.Students[0].FullName == "" || got.Counts() != (domain.RosterCounts{Present: 1, Unmarked: 1}) {
		t.Errorf("the register reads %+v, counts %+v", got.Students, got.Counts())
	}
	if _, err := w.app.Queries.SessionAttendance.Handle(context.Background(), query.SessionAttendance{Scope: by(w.b).Scope, ViewerID: w.b, SessionID: session}); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("another teacher's register: %v, want not found", err)
	}
	future := w.create(w.a, class, once("2026-12-01", "08:00")).ID.String()
	if r := w.register(by(w.a), future); len(r.Students) != 1 || r.Session.HasAttendance {
		t.Errorf("a future session's register is %+v", r)
	}
}

// TestAnArchivedClassHasNoSessionFromItsArchivingOn archives a class at noon
// on 20 November. The register list and its badge keep the Mondays before
// (the 16th still marked) and drop this morning's, marked absent, and a
// one-off of the 21st; the rate leaves this morning's mark out; saving or reading either answers not found, as does the
// calendar and the range read; restoring the class brings them back.
func TestAnArchivedClassHasNoSessionFromItsArchivingOn(t *testing.T) {
	w := newWorld(t)
	class := w.class(w.a, nil)
	student := w.student()
	w.member(class, student, at("2026-09-01 08:00"))
	series := w.create(w.a, class, weekly("2026-11-02", "08:00")).Series
	oneOff := w.create(w.a, class, once("2026-11-21", "08:00")).ID.String()
	w.save(w.a, occurrence(series, "2026-11-16"), set(student, domain.Present))
	w.save(w.a, occurrence(series, "2026-11-23"), set(student, domain.Absent))
	list := func() domain.SessionPage {
		w.t.Helper()
		page, err := w.app.Queries.AttendanceSessions.Handle(context.Background(), query.AttendanceSessions{Scope: by(w.a).Scope, ClassID: &class})
		if err != nil {
			w.t.Fatal(err)
		}
		return page
	}
	if before := list(); len(before.Items) != 5 || before.NotMarked != 3 {
		t.Fatalf("before archiving the list holds %v, notMarked %d", listedIDs(before), before.NotMarked)
	}

	w.exec(`UPDATE app.classes SET archived_at = $2 WHERE id = $1::uuid`, class, at("2026-11-20 12:00"))
	after := list()
	if want := []string{occurrence(series, "2026-11-16"), occurrence(series, "2026-11-09"), occurrence(series, "2026-11-02")}; !slices.Equal(listedIDs(after), want) || after.NotMarked != 2 {
		t.Errorf("after archiving the list holds %v, notMarked %d, want %v and 2", listedIDs(after), after.NotMarked, want)
	}
	for _, gone := range []string{occurrence(series, "2026-11-23"), oneOff} {
		if _, err := w.trySave(by(w.a), gone, set(student, domain.Present)); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("saving %s after archiving: %v, want not found", gone, err)
		}
		if _, err := w.app.Queries.SessionAttendance.Handle(context.Background(), query.SessionAttendance{Scope: by(w.a).Scope, ViewerID: w.a, SessionID: gone}); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("reading %s after archiving: %v, want not found", gone, err)
		}
	}
	if kept := w.register(by(w.a), occurrence(series, "2026-11-16")); rates(kept)[student] != 100 || !kept.Session.HasAttendance {
		t.Errorf("the 16th's register after archiving reads %v", rates(kept))
	}
	view, err := w.app.Queries.Calendar.Handle(context.Background(), query.Calendar{Scope: by(w.a).Scope, ViewerID: w.a, ClassID: class, From: at("2026-11-16 00:00"), To: at("2026-12-01 00:00")})
	if err != nil {
		t.Fatal(err)
	}
	var calendar []string
	for _, s := range view.Sessions {
		calendar = append(calendar, s.ID.String())
	}
	if want := []string{occurrence(series, "2026-11-16")}; !slices.Equal(calendar, want) {
		t.Errorf("the calendar after archiving holds %v, want %v", calendar, want)
	}
	if week := w.week(w.a, "2026-11-23"); len(week) != 0 {
		t.Errorf("the week after archiving holds %+v", week)
	}

	w.exec(`UPDATE app.classes SET archived_at = NULL WHERE id = $1::uuid`, class)
	if restored := list(); len(restored.Items) != 5 || restored.NotMarked != 3 {
		t.Errorf("after restoring the list holds %v, notMarked %d", listedIDs(restored), restored.NotMarked)
	}
	w.save(w.a, oneOff, set(student, domain.Late))
}

// TestAMemberPromotedToStaffLeavesTheRegister promotes a member to teacher:
// they leave the roster and the list's student count, and a save naming them
// is refused; a student marked before keeps the mark.
func TestAMemberPromotedToStaffLeavesTheRegister(t *testing.T) {
	w := newWorld(t)
	class := w.class(w.a, nil)
	student, promoted := w.student(), w.student()
	w.member(class, student, at("2026-09-01 08:00"))
	w.member(class, promoted, at("2026-09-02 08:00"))
	session := w.create(w.a, class, once("2026-11-23", "08:00")).ID.String()
	w.exec(`UPDATE app.users SET role_id = (SELECT id FROM app.roles WHERE builtin_key = 'teacher') WHERE id = $1::uuid`, promoted)
	got := w.register(by(w.a), session)
	if len(got.Students) != 1 || got.Students[0].StudentID != student {
		t.Errorf("the roster is %+v, want the student only", got.Students)
	}
	var refused *domain.RefusedMarks
	if _, err := w.trySave(by(w.a), session, set(promoted, domain.Present)); !errors.As(err, &refused) || !slices.Equal(refused.StudentIDs, []string{promoted}) {
		t.Errorf("marking the promoted member: %v, want refused", err)
	}
	page, err := w.app.Queries.AttendanceSessions.Handle(context.Background(), query.AttendanceSessions{Scope: by(w.a).Scope, ClassID: &class})
	if err != nil || len(page.Items) != 1 || page.Items[0].StudentCount != 1 {
		t.Errorf("the list reads %+v (%v), want one session of one student", page.Items, err)
	}
}
