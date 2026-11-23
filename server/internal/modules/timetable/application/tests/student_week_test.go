//go:build integration

package application_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"quizzivy/internal/core/adapters"
	assignmentsapp "quizzivy/internal/modules/assignments/application"
	assignmentsrepo "quizzivy/internal/modules/assignments/repositories"
	"quizzivy/internal/modules/timetable/application/query"
	"quizzivy/internal/modules/timetable/domain"
	"quizzivy/internal/platform/db"
)

// weekWorld reads, beside the timetable, the assignments due through the
// assignments module's student query and the adapter the server wires.
func weekWorld(t *testing.T) (*world, *assignmentsrepo.Postgres) {
	t.Helper()
	w := newWorld(t)
	store := assignmentsrepo.NewPostgres(db.NewContext(w.tx))
	w.app.WithDueItems(adapters.TimetableDueItems{Query: assignmentsapp.New(store).Queries.ForStudent})
	return w, store
}

func (w *world) studentWeek(student string, start *string) domain.Week {
	w.t.Helper()
	q := query.StudentWeek{StudentID: student}
	if start != nil {
		on := day(*start)
		q.Start = &on
	}
	week, err := w.app.Queries.StudentWeek.Handle(context.Background(), q)
	if err != nil {
		w.t.Fatalf("the week of %s: %v", student, err)
	}
	return week
}

// laidOut is each day of week that holds something, as its items: a session
// as its id, a due item as "due <id> <close> <state> <limit>".
func laidOut(week domain.Week) map[string][]string {
	out := map[string][]string{}
	for _, d := range week.Days {
		for _, item := range d.Items {
			key := d.Date.Format(time.DateOnly)
			if item.Session != nil {
				out[key] = append(out[key], item.Session.ID.String())
				continue
			}
			state, limit := "not_started", "none"
			if item.Due.Live {
				state = "in_progress"
			}
			if item.Due.DurationMinutes != nil {
				limit = time.Duration(*item.Due.DurationMinutes * int(time.Minute)).String()
			}
			out[key] = append(out[key], "due "+item.Due.ID+" "+item.Due.ClosesAt.In(hcm).Format("15:04")+" "+state+" "+limit)
		}
	}
	return out
}

// TestAStudentWeekRunsMondayToSundayInUTCPlus7 reads the week of a student
// in Asia/Ho_Chi_Minh: Monday 06:00 there is Sunday in UTC and opens the
// week, Sunday 23:30 closes it, and the Sundays either side are other weeks.
// Any day of the week, Sunday included, names its Monday; with no date, the
// week is today's in the student's zone, already Monday there while it is
// still Sunday in UTC. A student in UTC reads the same sessions on other days.
func TestAStudentWeekRunsMondayToSundayInUTCPlus7(t *testing.T) {
	w, _ := weekWorld(t)
	w.now = at("2026-11-20 10:00")
	class, student := w.class(w.a, nil), w.student()
	w.member(class, student, w.now)
	w.create(w.a, class, once("2026-11-22", "23:30"))
	opening := w.create(w.a, class, once("2026-11-23", "06:00"))
	closing := w.create(w.a, class, once("2026-11-29", "23:30"))
	next := w.create(w.a, class, once("2026-11-30", "00:30"))

	want := map[string][]string{
		"2026-11-23": {opening.ID.String()},
		"2026-11-29": {closing.ID.String()},
	}
	for _, start := range []string{"2026-11-23", "2026-11-26", "2026-11-29"} {
		week := w.studentWeek(student, &start)
		if week.Start.Format(time.DateOnly) != "2026-11-23" || week.End.Format(time.DateOnly) != "2026-11-29" || len(week.Days) != 7 {
			t.Errorf("the week of %s runs %s to %s over %d days", start, week.Start.Format(time.DateOnly), week.End.Format(time.DateOnly), len(week.Days))
		}
		if got := laidOut(week); !reflect.DeepEqual(got, want) {
			t.Errorf("the week of %s holds %v, want %v", start, got, want)
		}
	}

	w.now = time.Date(2026, 11, 29, 18, 0, 0, 0, time.UTC)
	current := w.studentWeek(student, nil)
	if current.Start.Format(time.DateOnly) != "2026-11-30" {
		t.Errorf("on Sunday 18:00 UTC the current week starts on %s, want Monday 30 November in UTC+7", current.Start.Format(time.DateOnly))
	}
	if got := laidOut(current); !reflect.DeepEqual(got, map[string][]string{"2026-11-30": {next.ID.String()}}) {
		t.Errorf("the current week holds %v", got)
	}

	w.zones[student] = "UTC"
	start := "2026-11-26"
	if got := laidOut(w.studentWeek(student, &start)); !reflect.DeepEqual(got, map[string][]string{
		"2026-11-29": {closing.ID.String(), next.ID.String()},
	}) {
		t.Errorf("in UTC the week holds %v, want the last two sessions on its Sunday", got)
	}
}

// TestAnOverrideMovesOneStudentsCloseIntoTheWeek gives one of two classmates
// a later close on an assignment that closed last week: it is due in their
// week on their own close and time limit, and absent from the other's. An
// assignment closing this week is due for both on its own close, one closing
// next week in neither, and a flexible one carries no time limit.
func TestAnOverrideMovesOneStudentsCloseIntoTheWeek(t *testing.T) {
	w, store := weekWorld(t)
	class, extended, other := w.class(w.a, nil), w.student(), w.student()
	w.member(class, extended, w.now)
	w.member(class, other, w.now)
	late := w.assignment(store, w.a, []string{class}, at("2026-11-16 08:00"), at("2026-11-20 17:00"))
	w.exec(`INSERT INTO app.assignment_student_overrides (assignment_id, student_id, closes_at, duration_minutes, reason)
		VALUES ($1::uuid, $2::uuid, $3, 60, 'Ốm')`, late, extended, at("2026-11-26 17:00"))
	this := w.assignment(store, w.a, []string{class}, at("2026-11-22 08:00"), at("2026-11-25 17:00"))
	flexible := w.assignment(store, w.a, []string{class}, at("2026-11-24 08:00"), at("2026-11-28 20:00"))
	w.exec(`UPDATE app.assignments SET mode = 'flexible' WHERE id = $1::uuid`, flexible)
	w.assignment(store, w.a, []string{class}, at("2026-11-30 08:00"), at("2026-12-02 17:00"))

	start := "2026-11-23"
	shared := map[string][]string{
		"2026-11-25": {"due " + this + " 17:00 not_started 45m0s"},
		"2026-11-28": {"due " + flexible + " 20:00 not_started none"},
	}
	mine := map[string][]string{"2026-11-26": {"due " + late + " 17:00 not_started 1h0m0s"}}
	for k, v := range shared {
		mine[k] = v
	}
	if got := laidOut(w.studentWeek(extended, &start)); !reflect.DeepEqual(got, mine) {
		t.Errorf("the extended student's week holds %v, want %v", got, mine)
	}
	if got := laidOut(w.studentWeek(other, &start)); !reflect.DeepEqual(got, shared) {
		t.Errorf("the classmate's week holds %v, want %v", got, shared)
	}
}

// TestAClassTheStudentLeftLeavesTheirWeek has a student in two classes leave
// one: that class's sessions and the assignment that reached them through it
// leave their week, the other class's stay.
func TestAClassTheStudentLeftLeavesTheirWeek(t *testing.T) {
	w, store := weekWorld(t)
	left, kept, student := w.class(w.a, nil), w.class(w.b, nil), w.student()
	w.member(left, student, w.now)
	w.member(kept, student, w.now)
	gone := w.create(w.a, left, weekly("2026-11-24", "18:00"))
	stays := w.create(w.b, kept, weekly("2026-11-25", "18:00"))
	assigned := w.assignment(store, w.a, []string{left}, at("2026-11-22 08:00"), at("2026-11-27 17:00"))

	start := "2026-11-23"
	if got := laidOut(w.studentWeek(student, &start)); !reflect.DeepEqual(got, map[string][]string{
		"2026-11-24": {gone.ID.String()}, "2026-11-25": {stays.ID.String()}, "2026-11-27": {"due " + assigned + " 17:00 not_started 45m0s"},
	}) {
		t.Fatalf("before leaving the week holds %v", got)
	}
	w.exec(`DELETE FROM app.class_members WHERE class_id = $1::uuid AND user_id = $2::uuid`, left, student)
	if got := laidOut(w.studentWeek(student, &start)); !reflect.DeepEqual(got, map[string][]string{"2026-11-25": {stays.ID.String()}}) {
		t.Errorf("after leaving the week holds %v, want only the other class's session", got)
	}
}

// TestAnArchivedClassLeavesTheWeek archives a class on Wednesday: its
// teacher still reads Monday's session, held before the archiving, but the
// student's week holds none of the class's sessions, while their other class
// stays. Restoring the class brings the sessions back.
func TestAnArchivedClassLeavesTheWeek(t *testing.T) {
	w, _ := weekWorld(t)
	archived, other, student := w.class(w.a, nil), w.class(w.a, nil), w.student()
	w.member(archived, student, w.now)
	w.member(other, student, w.now)
	monday := w.create(w.a, archived, weekly("2026-11-23", "18:00"))
	friday := w.create(w.a, archived, weekly("2026-11-27", "18:00"))
	stays := w.create(w.a, other, weekly("2026-11-24", "18:00"))
	w.exec(`UPDATE app.classes SET archived_at = $2 WHERE id = $1::uuid`, archived, at("2026-11-25 12:00"))

	if got := ids(w.week(w.a, "2026-11-23")); !reflect.DeepEqual(got, []string{monday.ID.String(), stays.ID.String()}) {
		t.Fatalf("the teacher's week holds %v, want Monday's session before the archiving and the other class's", got)
	}
	start := "2026-11-23"
	if got := laidOut(w.studentWeek(student, &start)); !reflect.DeepEqual(got, map[string][]string{"2026-11-24": {stays.ID.String()}}) {
		t.Errorf("the student's week holds %v, want only the class that is not archived", got)
	}
	w.exec(`UPDATE app.classes SET archived_at = NULL WHERE id = $1::uuid`, archived)
	if got := laidOut(w.studentWeek(student, &start)); !reflect.DeepEqual(got, map[string][]string{
		"2026-11-23": {monday.ID.String()}, "2026-11-24": {stays.ID.String()}, "2026-11-27": {friday.ID.String()},
	}) {
		t.Errorf("after restoring the week holds %v", got)
	}
}

func ids(sessions []domain.Session) []string {
	out := make([]string, len(sessions))
	for i, s := range sessions {
		out[i] = s.ID.String()
	}
	return out
}

// TestACancelledSessionStaysInTheWeekFlagged cancels one date of a weekly
// session and moves another with a note and a room: the cancelled one stays,
// flagged, and the other reads its date's room and note, the series' room
// otherwise.
func TestACancelledSessionStaysInTheWeekFlagged(t *testing.T) {
	w, _ := weekWorld(t)
	class, student := w.class(w.a, ptr("P.101")), w.student()
	w.member(class, student, w.now)
	monday := w.create(w.a, class, weekly("2026-11-23", "18:00"))
	thursday := w.create(w.a, class, weekly("2026-11-26", "18:00"))
	w.patch(w.a, monday.ID.String(), domain.Patch{Cancelled: ptr(true)})
	w.patch(w.a, thursday.ID.String(), domain.Patch{Room: domain.TextPatch{Set: true, Value: ptr("P.305")}, Note: domain.TextPatch{Set: true, Value: ptr("Mang sách")}})

	start := "2026-11-23"
	week := w.studentWeek(student, &start)
	if got := laidOut(week); !reflect.DeepEqual(got, map[string][]string{
		"2026-11-23": {monday.ID.String()}, "2026-11-26": {thursday.ID.String()},
	}) {
		t.Fatalf("the week holds %v", got)
	}
	cancelled, moved := week.Days[0].Items[0].Session, week.Days[3].Items[0].Session
	if !cancelled.Cancelled || cancelled.Room == nil || *cancelled.Room != "P.101" {
		t.Errorf("Monday reads cancelled %v in %v, want cancelled in the class room", cancelled.Cancelled, cancelled.Room)
	}
	if moved.Cancelled || moved.Room == nil || *moved.Room != "P.305" || moved.Note == nil || *moved.Note != "Mang sách" {
		t.Errorf("Thursday reads cancelled %v, room %v, note %v", moved.Cancelled, moved.Room, moved.Note)
	}
}

// TestAStudentWeekTakesFourQueriesWhateverTheNumberOfClasses reads one
// class, then six, each with a weekly series, a cancelled date and an
// assignment, in the same four queries: the student's classes, the series,
// the rows and the assignments due.
func TestAStudentWeekTakesFourQueriesWhateverTheNumberOfClasses(t *testing.T) {
	w, store := weekWorld(t)
	student := w.student()
	add := func() {
		class := w.class(w.a, nil)
		w.member(class, student, w.now)
		s := w.create(w.a, class, weekly("2026-11-24", "18:00"))
		w.patch(w.a, domain.OccurrenceID(s.Series.ID, day("2026-11-24")).String(), domain.Patch{Cancelled: ptr(true)})
		w.assignment(store, w.a, []string{class}, at("2026-11-20 08:00"), at("2026-11-27 17:00"))
	}
	start := "2026-11-23"
	read := func() (int, int) {
		before := w.queries.count()
		week := w.studentWeek(student, &start)
		return w.queries.count() - before, len(week.Days[1].Items) + len(week.Days[4].Items)
	}
	add()
	if n, items := read(); n != 4 || items != 2 {
		t.Errorf("one class took %d queries for %d items, want 4 and 2", n, items)
	}
	for range 5 {
		add()
	}
	if n, items := read(); n != 4 || items != 12 {
		t.Errorf("six classes took %d queries for %d items, want 4 and 12", n, items)
	}
	alone := w.student()
	before := w.queries.count()
	w.studentWeek(alone, &start)
	if n := w.queries.count() - before; n != 2 {
		t.Errorf("a student in no class took %d queries, want 2: their classes and the assignments due", n)
	}
}
