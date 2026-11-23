//go:build integration

package repositories_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	dashboardapp "quizzivy/internal/modules/dashboard/application"
	"quizzivy/internal/modules/dashboard/application/query"
	"quizzivy/internal/modules/dashboard/repositories"
	timetableapp "quizzivy/internal/modules/timetable/application"
	"quizzivy/internal/modules/timetable/application/command"
	timetabledomain "quizzivy/internal/modules/timetable/domain"
	timetablerepo "quizzivy/internal/modules/timetable/repositories"
	"quizzivy/internal/platform/db"
	"quizzivy/internal/shared/access"
	"quizzivy/internal/shared/actor"
)

type actorZones map[string]string

func (z actorZones) ZoneOf(_ context.Context, userID string) (string, error) { return z[userID], nil }

// TestTodayListsTheActorsSessionsOfTheirDayWithoutTheCancelledOnes wires the
// dashboard to the timetable as the server does, over Postgres. On Monday 5
// October at 12:00 UTC, the teacher in Asia/Ho_Chi_Minh reads that Monday's
// sessions there, and the same teacher in America/New_York reads Monday in
// New York, which takes the session at 06:00 on Tuesday in Ho Chi Minh City.
// A cancelled date and another teacher's session are left out, and every item
// is in order of time beside the assignment items.
func TestTodayListsTheActorsSessionsOfTheirDayWithoutTheCancelledOnes(t *testing.T) {
	w := newHomeWorld(t)
	now := fixedNow()
	hcm, err := time.LoadLocation("Asia/Ho_Chi_Minh")
	if err != nil {
		t.Fatal(err)
	}
	execHome(t, w, `UPDATE app.classes SET room = 'P.204' WHERE id = $1`, w.classB)
	timetable := timetableapp.New(timetablerepo.NewPostgres(db.NewContext(w.tx)))
	timetable.SetClock(func() time.Time { return now })
	create := func(teacher, class, date, start string, weekly bool) timetabledomain.Session {
		t.Helper()
		clock, err := timetabledomain.ParseClock(start)
		if err != nil {
			t.Fatal(err)
		}
		day, err := time.Parse(time.DateOnly, date)
		if err != nil {
			t.Fatal(err)
		}
		s, err := timetable.Commands.CreateSession.Handle(context.Background(), command.CreateSession{
			Actor: actor.Actor{ID: teacher, Scope: access.Scope{UserID: teacher}}, ClassID: class,
			Draft: timetabledomain.Draft{Date: day, Start: clock, DurationMinutes: 90, RepeatWeekly: weekly},
		})
		if err != nil {
			t.Fatalf("a session of %s on %s at %s: %v", class, date, start, err)
		}
		return s
	}
	morning := create(w.b, w.classB, "2026-10-05", "08:00", false) // 01:00 UTC; Sunday 21:00 in New York
	evening := create(w.b, w.classB, "2026-10-05", "22:00", false) // 15:00 UTC
	tuesday := create(w.b, w.classB, "2026-10-06", "06:00", false) // 23:00 UTC Monday
	series := create(w.b, w.classB, "2026-10-05", "18:00", true)   // 11:00 UTC, cancelled below
	foreign := create(w.a, w.classA, "2026-10-05", "19:00", false) // another teacher's
	cancelled := timetabledomain.OccurrenceID(series.Series.ID, time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)).String()
	if _, err := timetable.Commands.UpdateSession.Handle(context.Background(), command.UpdateSession{
		Actor: actor.Actor{ID: w.b, Scope: access.Scope{UserID: w.b}}, SessionID: cancelled, Patch: timetabledomain.Patch{Cancelled: new(true)},
	}); err != nil {
		t.Fatal(err)
	}

	zones := actorZones{w.b: "Asia/Ho_Chi_Minh"}
	app := dashboardapp.New(repositories.NewPostgres(db.NewContext(w.tx))).WithZones(zones).WithSessions(timetable.Queries.Occurrences)
	app.SetClock(func() time.Time { return now })
	read := func() []string {
		t.Helper()
		out, err := app.Queries.Summary.Handle(context.Background(), query.Summary{Scope: access.Scope{UserID: w.b}})
		if err != nil {
			t.Fatal(err)
		}
		var sessions []string
		for i, row := range out.Today {
			if i > 0 && row.At.Before(out.Today[i-1].At) {
				t.Errorf("the day is out of order at %d: %+v", i, out.Today)
			}
			if row.Session == nil {
				continue
			}
			if row.Kind != "session" || row.Session.ClassID != w.classB || row.Session.Room == nil || *row.Session.Room != "P.204" {
				t.Errorf("a session of the day reads %+v %+v", row, row.Session)
			}
			sessions = append(sessions, row.Session.ID+" "+row.At.In(hcm).Format("01-02 15:04"))
		}
		return sessions
	}
	if got, want := read(), []string{morning.ID.String() + " 10-05 08:00", evening.ID.String() + " 10-05 22:00"}; !reflect.DeepEqual(got, want) {
		t.Errorf("in Ho Chi Minh City the day holds %v, want %v (not %s, cancelled, nor %s, another teacher's)", got, want, cancelled, foreign.ID)
	}
	zones[w.b] = "America/New_York"
	if got, want := read(), []string{evening.ID.String() + " 10-05 22:00", tuesday.ID.String() + " 10-06 06:00"}; !reflect.DeepEqual(got, want) {
		t.Errorf("in New York the day holds %v, want %v", got, want)
	}
}
