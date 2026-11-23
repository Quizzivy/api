//go:build integration

package repositories_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"quizzivy/internal/modules/assignments/domain"
	"quizzivy/internal/shared/access"
)

type calendarWorld struct {
	*reachWorld
	from, to time.Time
	names    map[string]string
}

func (w *calendarWorld) assign(t *testing.T, name string, req domain.Request, version string, classes, students []string, opens, closes time.Duration) {
	t.Helper()
	in := input(version, classes, students)
	in.OpensAt, in.ClosesAt = w.from.Add(opens), w.from.Add(closes)
	w.names[w.create(t, req, in)] = name
}

// read answers the windows scope reads, by the names this world gave them,
// leaving out every assignment it did not make.
func (w *calendarWorld) read(t *testing.T, scope access.Scope, classID string) map[string]domain.CalendarWindow {
	t.Helper()
	windows, err := w.store.CalendarWindows(context.Background(), domain.CalendarInput{Scope: scope, ClassID: classID, From: w.from, To: w.to})
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]domain.CalendarWindow{}
	for _, win := range windows {
		if name, ok := w.names[win.ID]; ok {
			out[name] = win
		}
	}
	return out
}

func keys(m map[string]domain.CalendarWindow) []string {
	out := []string{}
	for k := range m {
		out = append(out, k)
	}
	return sortedIDs(out...)
}

// TestTheCalendarReadsEachReachedAssignmentsOwnWindow reads, for a week, the
// published assignments a teacher reaches that open or close in it: one that
// opened before the week and closes inside it, one inside, one closed early
// inside it at its early close, and none that ends before it, spans it or is
// a draft, nor another teacher's; a student's later override moves nothing.
// Each names the target classes its reader teaches, every one under
// scope.all, and a class narrows the read to the assignments targeting it.
func TestTheCalendarReadsEachReachedAssignmentsOwnWindow(t *testing.T) {
	w := &calendarWorld{reachWorld: newReachWorld(t), names: map[string]string{}}
	w.from = time.Now().Truncate(time.Hour).Add(24 * time.Hour)
	w.to = w.from.Add(7 * 24 * time.Hour)
	day := 24 * time.Hour
	a, b, admin := as(w.a, false), as(w.b, false), as(w.admin, true)

	w.assign(t, "opened before", a, w.versionA, []string{w.classA}, nil, -2*day, 2*day)
	w.assign(t, "inside", a, w.versionA, []string{w.classA}, nil, day, 3*day)
	w.assign(t, "ended before", a, w.versionA, []string{w.classA}, nil, -5*day, -time.Minute)
	w.assign(t, "spans", a, w.versionA, []string{w.classA}, nil, -day, 10*day)
	w.assign(t, "closed early", a, w.versionA, []string{w.classA}, nil, -5*day, 20*day)
	w.assign(t, "by name", a, w.versionA, nil, []string{w.studentA}, day, 2*day)
	w.assign(t, "theirs", b, w.versionB, []string{w.classB}, nil, day, 2*day)
	w.assign(t, "both classes", admin, w.versionA, []string{w.classA, w.classB}, nil, 2*day, 4*day)
	draft := input(w.versionA, []string{w.classA}, nil)
	draft.OpensAt, draft.ClosesAt, draft.Draft = w.from.Add(day), w.from.Add(2*day), true
	w.names[w.create(t, a, draft)] = "draft"

	ids := map[string]string{}
	for id, name := range w.names {
		ids[name] = id
	}
	early := w.from.Add(2 * day)
	if _, err := w.tx.Exec(context.Background(), `UPDATE app.assignments SET closed_at = $2 WHERE id = $1::uuid`, ids["closed early"], early); err != nil {
		t.Fatal(err)
	}
	if _, err := w.tx.Exec(context.Background(), `INSERT INTO app.assignment_student_overrides (assignment_id, student_id, closes_at, reason)
		VALUES ($1::uuid, $2::uuid, $3, 'Ốm')`, ids["inside"], w.studentA, w.from.Add(30*day)); err != nil {
		t.Fatal(err)
	}

	mine := w.read(t, access.Scope{UserID: w.a}, "")
	if got, want := keys(mine), sortedIDs("opened before", "inside", "closed early", "by name", "both classes"); !reflect.DeepEqual(got, want) {
		t.Errorf("teacher A reads %v, want %v", got, want)
	}
	if got := mine["opened before"]; !got.OpensAt.Equal(w.from.Add(-2*day)) || !got.ClosesAt.Equal(w.from.Add(2*day)) || got.Title != "Đề" {
		t.Errorf("the one opened before the week reads %+v", got)
	}
	if got := mine["inside"].ClosesAt; !got.Equal(w.from.Add(3 * day)) {
		t.Errorf("the one a student's override extends closes at %v, want its own close", got)
	}
	if got := mine["closed early"].ClosesAt; !got.Equal(early) {
		t.Errorf("the one closed early closes at %v, want %v", got, early)
	}
	for name, want := range map[string][]string{"both classes": {"Lớp A"}, "by name": {}, "inside": {"Lớp A"}} {
		if got := mine[name].ClassNames; !reflect.DeepEqual(got, want) {
			t.Errorf("%s names %v for teacher A, want %v", name, got, want)
		}
	}

	if got, want := keys(w.read(t, access.Scope{UserID: w.b}, "")), sortedIDs("theirs", "both classes"); !reflect.DeepEqual(got, want) {
		t.Errorf("teacher B reads %v, want %v", got, want)
	}
	all := w.read(t, access.Scope{UserID: w.admin, All: true}, "")
	if got := all["both classes"].ClassNames; !reflect.DeepEqual(got, []string{"Lớp A", "Lớp B"}) {
		t.Errorf("under scope.all the shared one names %v", got)
	}
	if got, want := keys(w.read(t, access.Scope{UserID: w.a}, w.classA)), sortedIDs("opened before", "inside", "closed early", "both classes"); !reflect.DeepEqual(got, want) {
		t.Errorf("teacher A's class reads %v, want %v", got, want)
	}
	if got := w.read(t, access.Scope{}, ""); len(got) != 0 {
		t.Errorf("no one reads %v", keys(got))
	}
}
