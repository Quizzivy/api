package application_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"quizzivy/internal/modules/dashboard/application"
	"quizzivy/internal/modules/dashboard/application/query"
	"quizzivy/internal/modules/dashboard/domain"
	timetablequery "quizzivy/internal/modules/timetable/application/query"
	timetabledomain "quizzivy/internal/modules/timetable/domain"
	"quizzivy/internal/shared/access"
	"quizzivy/internal/shared/cqrs"
)

type zoneOf string

func (z zoneOf) ZoneOf(context.Context, string) (string, error) { return string(z), nil }

type todayStore struct {
	store
	today []domain.Today
}

func (s *todayStore) Home(_ context.Context, q domain.HomeQuery) (domain.Home, error) {
	s.home = q
	return domain.Home{Today: s.today}, nil
}

// TestTodayAsksForTheActorsDayAndLeavesCancelledSessionsOut reads the day of
// an actor in America/New_York: the timetable is asked for that day's
// midnight to the next, in the dashboard's own scope, and what it answers
// joins the assignment items by time, a cancelled session left out. Without
// the timetable the day lists the assignments only.
func TestTodayAsksForTheActorsDayAndLeavesCancelledSessionsOut(t *testing.T) {
	now := time.Date(2026, 11, 23, 3, 0, 0, 0, time.UTC)
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	at := func(clock string) time.Time {
		v, err := time.ParseInLocation("2006-01-02 15:04", clock, ny)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	repo := &todayStore{store: store{t: t}, today: []domain.Today{
		{Kind: "opens", At: at("2026-11-22 08:00"), AssignmentID: "opens", Title: "Đề mở"},
		{Kind: "closes", At: at("2026-11-22 20:00"), AssignmentID: "closes", Title: "Đề đóng"},
	}}
	room := "P.204"
	var asked []timetablequery.Occurrences
	sessions := cqrs.HandlerFunc[timetablequery.Occurrences, []timetabledomain.Session](
		func(_ context.Context, q timetablequery.Occurrences) ([]timetabledomain.Session, error) {
			asked = append(asked, q)
			return []timetabledomain.Session{
				{ID: timetabledomain.OneOffID("morning"), ClassID: "class-1", ClassName: "IELTS A", StartsAt: at("2026-11-22 07:00"), Room: &room},
				{ID: timetabledomain.OneOffID("cancelled"), ClassID: "class-1", ClassName: "IELTS A", StartsAt: at("2026-11-22 12:00"), Cancelled: true},
				{ID: timetabledomain.OneOffID("evening"), ClassID: "class-2", ClassName: "TOEIC", StartsAt: at("2026-11-22 18:30")},
			}, nil
		})
	app := application.New(repo).WithZones(zoneOf("America/New_York")).WithSessions(sessions)
	app.SetClock(func() time.Time { return now })
	scope := access.Scope{UserID: "teacher"}

	out, err := app.Queries.Summary.Handle(context.Background(), query.Summary{Scope: scope})
	if err != nil {
		t.Fatal(err)
	}
	want := timetablequery.Occurrences{Scope: scope, ViewerID: "teacher", From: at("2026-11-22 00:00"), To: at("2026-11-23 00:00")}
	if len(asked) != 1 || !reflect.DeepEqual(asked[0], want) {
		t.Fatalf("asked the timetable %+v, want %+v", asked, want)
	}
	var got []string
	for _, row := range out.Today {
		if row.Session != nil {
			got = append(got, row.Kind+" "+row.Session.ID+" "+row.Session.ClassName)
			continue
		}
		got = append(got, row.Kind+" "+row.AssignmentID)
	}
	if wantDay := []string{"session morning IELTS A", "opens opens", "session evening TOEIC", "closes closes"}; !reflect.DeepEqual(got, wantDay) {
		t.Errorf("the day reads %v, want %v", got, wantDay)
	}
	if first := out.Today[0].Session; first.ClassID != "class-1" || first.Room == nil || *first.Room != room || !out.Today[0].At.Equal(at("2026-11-22 07:00")) {
		t.Errorf("the first session reads %+v at %s", first, out.Today[0].At)
	}

	bare := application.New(repo).WithZones(zoneOf("America/New_York"))
	bare.SetClock(func() time.Time { return now })
	alone, err := bare.Queries.Summary.Handle(context.Background(), query.Summary{Scope: scope})
	if err != nil {
		t.Fatal(err)
	}
	if len(alone.Today) != 2 || alone.Today[0].Session != nil || alone.Today[1].Session != nil {
		t.Errorf("without the timetable the day reads %+v", alone.Today)
	}
}
