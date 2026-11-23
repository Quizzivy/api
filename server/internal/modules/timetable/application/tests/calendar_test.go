//go:build integration

package application_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	"quizzivy/internal/core/adapters"
	assignmentsapp "quizzivy/internal/modules/assignments/application"
	assignmentsdomain "quizzivy/internal/modules/assignments/domain"
	assignmentsrepo "quizzivy/internal/modules/assignments/repositories"
	organizationmodel "quizzivy/internal/modules/organization/application/model"
	"quizzivy/internal/modules/timetable/application/query"
	"quizzivy/internal/modules/timetable/domain"
	"quizzivy/internal/platform/db"
	"quizzivy/internal/shared/validation"
)

type termsAhead struct {
	asked []time.Time
	list  []organizationmodel.Term
}

func (t *termsAhead) TermsEndingFrom(_ context.Context, on time.Time) ([]organizationmodel.Term, error) {
	t.asked = append(t.asked, on)
	return t.list, nil
}

// calendarWorld draws, beside the timetable, the assignment windows the
// assignments module reads through the adapter the server wires.
func calendarWorld(t *testing.T) (*world, *termsAhead, *assignmentsrepo.Postgres) {
	t.Helper()
	w := newWorld(t)
	ahead := &termsAhead{}
	store := assignmentsrepo.NewPostgres(db.NewContext(w.tx))
	w.app.WithCalendar(ahead, adapters.TimetableAssignments{Query: assignmentsapp.New(store).Queries.Calendar})
	return w, ahead, store
}

func (w *world) className(class string) string {
	w.t.Helper()
	return w.id(`SELECT name FROM app.classes WHERE id = $1::uuid`, class)
}

// assignment publishes a test of teacher's to classes for [opens, closes).
func (w *world) assignment(store *assignmentsrepo.Postgres, teacher string, classes []string, opens, closes time.Time) string {
	w.t.Helper()
	test := w.id(`INSERT INTO app.tests (title, status, current_version, created_by, owner_id)
		VALUES ('Bài kiểm tra ' || $2, 'published', 1, $1, $1) RETURNING id::text`, teacher, w.marker)
	version := w.id(`INSERT INTO app.test_versions (test_id, version, total_points, published_by) VALUES ($1, 1, 10, $2) RETURNING id::text`, test, teacher)
	created, err := store.Create(context.Background(), assignmentsdomain.Request{ActorID: teacher}, assignmentsdomain.WriteInput{
		TestVersionID: version, ClassIDs: classes, OpensAt: opens, ClosesAt: closes, DurationMin: 45, MaxAttempts: 1,
		Review:    assignmentsdomain.Review{Release: assignmentsdomain.ReleaseOnSubmit},
		Integrity: assignmentsdomain.Integrity{OnLimitExceeded: "flag", MinAwayMs: 3000}, Now: time.Now(),
	})
	if err != nil {
		w.t.Fatalf("assigning to %v: %v", classes, err)
	}
	return created.ID
}

func (w *world) calendar(who, classID, from, to string) (query.CalendarView, error) {
	w.t.Helper()
	return w.app.Queries.Calendar.Handle(context.Background(), query.Calendar{
		Scope: by(who).Scope, ViewerID: who, ClassID: classID, From: day(from), To: day(to),
	})
}

func (w *world) mustCalendar(who, classID, from, to string) query.CalendarView {
	w.t.Helper()
	view, err := w.calendar(who, classID, from, to)
	if err != nil {
		w.t.Fatalf("the calendar of %s from %s to %s: %v", who, from, to, err)
	}
	return view
}

func sessionIDs(view query.CalendarView) []string {
	out := []string{}
	for _, s := range view.Sessions {
		out = append(out, s.ID.String())
	}
	return out
}

type drawn struct {
	kind domain.EventKind
	id   string
	at   string
}

func instant(s string) string { return at(s).UTC().Format(time.RFC3339) }

func eventsOf(view query.CalendarView) []drawn {
	out := []drawn{}
	for _, e := range view.Events {
		out = append(out, drawn{e.Kind, e.AssignmentID, e.At.UTC().Format(time.RFC3339)})
	}
	return out
}

// TestTheCalendarDrawsTheCallersSessionsAndAssignments reads two weeks of a
// teacher's two classes: a weekly session with one date cancelled, returned
// cancelled, a one-off, and the assignments of both classes, one that opened
// before the range drawn by its close alone. One class narrows both to it.
// Another teacher sees none of it, and naming the first teacher's class
// answers not found, as an unknown class does. The terms ahead of the first
// day come with it.
func TestTheCalendarDrawsTheCallersSessionsAndAssignments(t *testing.T) {
	w, ahead, store := calendarWorld(t)
	first, second := w.class(w.a, nil), w.class(w.a, nil)
	monday := w.create(w.a, first, weekly("2026-11-23", "18:00"))
	cancelled := domain.OccurrenceID(monday.Series.ID, day("2026-11-30")).String()
	w.patch(w.a, cancelled, domain.Patch{Cancelled: ptr(true)})
	oneOff := w.create(w.a, second, once("2026-11-25", "08:30"))
	opened := w.assignment(store, w.a, []string{first}, at("2026-11-20 08:00"), at("2026-11-26 17:00"))
	later := w.assignment(store, w.a, []string{second}, at("2026-12-01 07:00"), at("2026-12-10 17:00"))
	ahead.list = []organizationmodel.Term{{ID: "term", Name: "Học kỳ 1", StartsOn: day("2026-09-01"), EndsOn: day("2027-01-15")}}

	theirs := w.class(w.b, nil)
	w.create(w.b, theirs, weekly("2026-11-24", "18:00"))
	w.assignment(store, w.b, []string{theirs}, at("2026-11-24 08:00"), at("2026-11-27 17:00"))

	view := w.mustCalendar(w.a, "", "2026-11-23", "2026-12-07")
	wantSessions := []string{monday.ID.String(), oneOff.ID.String(), cancelled}
	if got := sessionIDs(view); !reflect.DeepEqual(got, wantSessions) {
		t.Errorf("the two weeks hold %v, want %v", got, wantSessions)
	}
	for _, s := range view.Sessions {
		if s.Cancelled != (s.ID.String() == cancelled) {
			t.Errorf("%s reads cancelled %v", s.ID, s.Cancelled)
		}
	}
	wantEvents := []drawn{
		{domain.EventCloses, opened, instant("2026-11-26 17:00")},
		{domain.EventOpens, later, instant("2026-12-01 07:00")},
	}
	if got := eventsOf(view); !reflect.DeepEqual(got, wantEvents) {
		t.Errorf("the events read %v, want %v", got, wantEvents)
	}
	if got := view.Events[0].ClassNames; !reflect.DeepEqual(got, []string{w.className(first)}) {
		t.Errorf("the closing names %v", got)
	}
	if !reflect.DeepEqual(view.Terms, ahead.list) || len(ahead.asked) != 1 || !ahead.asked[0].Equal(day("2026-11-23")) {
		t.Errorf("the terms read %v, asked from %v", view.Terms, ahead.asked)
	}

	one := w.mustCalendar(w.a, first, "2026-11-23", "2026-12-07")
	if got := sessionIDs(one); !reflect.DeepEqual(got, []string{monday.ID.String(), cancelled}) {
		t.Errorf("the first class holds %v", got)
	}
	if got := eventsOf(one); !reflect.DeepEqual(got, wantEvents[:1]) {
		t.Errorf("the first class's events read %v", got)
	}

	other := w.mustCalendar(w.b, "", "2026-11-23", "2026-12-07")
	for _, id := range sessionIDs(other) {
		if slices.Contains(wantSessions, id) {
			t.Errorf("another teacher sees session %s", id)
		}
	}
	for _, e := range other.Events {
		if e.AssignmentID == opened || e.AssignmentID == later {
			t.Errorf("another teacher sees assignment %s", e.AssignmentID)
		}
	}
	if len(other.Sessions) != 2 || len(other.Events) != 2 {
		t.Errorf("another teacher's own calendar holds %d sessions and %d events, want 2 and 2", len(other.Sessions), len(other.Events))
	}
	for name, class := range map[string]string{"the first teacher's class": first, "an unknown class": "019535d9-3df7-79fb-b466-fa907fa17f9e"} {
		if _, err := w.calendar(w.b, class, "2026-11-23", "2026-12-07"); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("%s answered %v, want not found", name, err)
		}
	}
}

// TestTheCalendarCoversOneToFortyTwoDays reads forty-two days and refuses
// forty-three, and an empty range, naming to.
func TestTheCalendarCoversOneToFortyTwoDays(t *testing.T) {
	w, _, _ := calendarWorld(t)
	w.create(w.a, w.class(w.a, nil), weekly("2026-11-23", "18:00"))
	if got := w.mustCalendar(w.a, "", "2026-11-23", "2027-01-04"); len(got.Sessions) != 6 {
		t.Errorf("forty-two days hold %d sessions, want 6", len(got.Sessions))
	}
	for name, to := range map[string]string{"forty-three days": "2027-01-05", "no day": "2026-11-23"} {
		_, err := w.calendar(w.a, "", "2026-11-23", to)
		var refused *validation.Error
		if !errors.As(err, &refused) || len(refused.Fields) != 1 || refused.Fields[0].Field != "to" {
			t.Errorf("%s answered %v, want a refusal naming to", name, err)
		}
	}
}

// TestTheCalendarTakesThreeQueriesWhateverTheNumberOfClasses reads one class
// and then ten, each with a weekly series, a cancelled date and an
// assignment, in the same three queries: the series, the rows and the
// assignment windows. Naming a class adds the one query that checks it.
func TestTheCalendarTakesThreeQueriesWhateverTheNumberOfClasses(t *testing.T) {
	w, _, store := calendarWorld(t)
	var classes []string
	add := func() {
		class := w.class(w.a, nil)
		classes = append(classes, class)
		s := w.create(w.a, class, weekly("2026-11-24", "18:00"))
		w.patch(w.a, domain.OccurrenceID(s.Series.ID, day("2026-12-01")).String(), domain.Patch{Cancelled: ptr(true)})
		w.assignment(store, w.a, []string{class}, at("2026-11-20 08:00"), at("2026-11-27 17:00"))
	}
	read := func(classID string) (int, query.CalendarView) {
		before := w.queries.count()
		view := w.mustCalendar(w.a, classID, "2026-11-23", "2027-01-04")
		return w.queries.count() - before, view
	}

	add()
	one, view := read("")
	if one != 3 || len(view.Sessions) != 6 || len(view.Events) != 1 {
		t.Errorf("one class took %d queries for %d sessions and %d events, want 3, 6 and 1", one, len(view.Sessions), len(view.Events))
	}
	for range 9 {
		add()
	}
	ten, view := read("")
	if ten != one || len(view.Sessions) != 60 || len(view.Events) != 10 {
		t.Errorf("ten classes took %d queries for %d sessions and %d events, want %d, 60 and 10", ten, len(view.Sessions), len(view.Events), one)
	}
	if named, view := read(classes[3]); named != one+1 || len(view.Sessions) != 6 || len(view.Events) != 1 {
		t.Errorf("one named class took %d queries for %d sessions and %d events, want %d, 6 and 1", named, len(view.Sessions), len(view.Events), one+1)
	}
}
