//go:build integration

package application_test

import (
	"context"
	"reflect"
	"sync"
	"testing"
	"time"

	"quizzivy/internal/modules/classes/application"
	"quizzivy/internal/modules/classes/application/command"
	"quizzivy/internal/modules/classes/application/query"
	"quizzivy/internal/modules/classes/domain"
)

// The join preview's weekly pattern and room (T-R8.7) are read only for a
// valid, usable code: every refusal answers before the class's series are
// asked for, exactly as it answered before R8.

// seriesSource is a ScheduleSource that answers fixed series for every class
// and records each class it was asked about.
type seriesSource struct {
	mu     sync.Mutex
	series []domain.WeeklySeries
	asked  [][]string
}

func (s *seriesSource) ActiveSeries(_ context.Context, classIDs []string, _ time.Time) (map[string][]domain.WeeklySeries, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.asked = append(s.asked, classIDs)
	out := map[string][]domain.WeeklySeries{}
	for _, id := range classIDs {
		if len(s.series) > 0 {
			out[id] = s.series
		}
	}
	return out, nil
}

func (s *seriesSource) calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.asked)
}

func tuesdayThursdayIn(room string) *seriesSource {
	return &seriesSource{series: []domain.WeeklySeries{
		{ScheduleSlot: domain.ScheduleSlot{Weekday: 4, StartTime: "18:00", DurationMinutes: 90}, Room: &room},
		{ScheduleSlot: domain.ScheduleSlot{Weekday: 2, StartTime: "18:00", DurationMinutes: 90}, Room: &room},
	}}
}

func scheduledSvc(t *testing.T, source *seriesSource) *application.Application {
	t.Helper()
	return newSvc(t, newPool(t)).WithSchedules(source)
}

func TestThePreviewOfAValidCodeCarriesTheWeeklyPatternAndTheDerivedRoom(t *testing.T) {
	pool := newPool(t)
	source := tuesdayThursdayIn("B2")
	svc := newSvc(t, pool).WithSchedules(source)
	classID, teacherID, _ := makeClassRow(t, pool)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `UPDATE app.classes SET schedule_label = 'Thứ 3, 5 · 18:00' WHERE id = $1`, classID); err != nil {
		t.Fatal(err)
	}
	code := issueCode(t, svc, classID, teacherID)

	got, err := svc.Queries.Preview.Handle(ctx, query.Preview{Code: code})
	if err != nil {
		t.Fatal(err)
	}
	wantSlots := []domain.ScheduleSlot{{Weekday: 2, StartTime: "18:00", DurationMinutes: 90}, {Weekday: 4, StartTime: "18:00", DurationMinutes: 90}}
	if got.Outcome != domain.PreviewOK || got.Schedule == nil || !reflect.DeepEqual(got.Schedule.Slots, wantSlots) ||
		got.Schedule.Label == nil || *got.Schedule.Label != "Thứ 3, 5 · 18:00" {
		t.Fatalf("the preview is %+v (schedule %+v), want the two weekly slots and the class's label", got, got.Schedule)
	}
	if got.Room == nil || *got.Room != "B2" {
		t.Errorf("the preview's room is %v, want the B2 every series shares, the class having none", got.Room)
	}
	if source.calls() != 1 || !reflect.DeepEqual(source.asked[0], []string{classID}) {
		t.Errorf("the series were asked for %v, want the one class the code reached, once", source.asked)
	}

	if _, err := pool.Exec(ctx, `UPDATE app.classes SET room = 'A1' WHERE id = $1`, classID); err != nil {
		t.Fatal(err)
	}
	got, err = svc.Queries.Preview.Handle(ctx, query.Preview{Code: code})
	if err != nil {
		t.Fatal(err)
	}
	if got.Room == nil || *got.Room != "A1" {
		t.Errorf("the preview's room is %v, want the class's own A1 over its series'", got.Room)
	}
}

func TestThePreviewOfAClassWithNeitherSlotsNorALabelHasNoSchedule(t *testing.T) {
	pool := newPool(t)
	svc := newSvc(t, pool).WithSchedules(&seriesSource{})
	classID, teacherID, _ := makeClassRow(t, pool)
	code := issueCode(t, svc, classID, teacherID)
	got, err := svc.Queries.Preview.Handle(context.Background(), query.Preview{Code: code})
	if err != nil {
		t.Fatal(err)
	}
	if got.Outcome != domain.PreviewOK || got.Schedule != nil || got.Room != nil {
		t.Errorf("the preview is %+v, want PreviewOK with no schedule and no room", got)
	}
}

func TestThePreviewOfARefusedCodeIsUnchangedAndReadsNoSeries(t *testing.T) {
	pool := newPool(t)
	source := tuesdayThursdayIn("B2")
	svc := newSvc(t, pool).WithSchedules(source)
	ctx := context.Background()
	for name, refuse := range map[string]struct {
		outcome domain.PreviewOutcome
		sql     string
	}{
		"expired": {domain.PreviewExpired, `UPDATE app.class_join_codes SET created_at = now() - interval '2 days', expires_at = now() - interval '1 day'
			  WHERE class_id = $1 AND revoked_at IS NULL`},
		"exhausted":                            {domain.PreviewExhausted, `UPDATE app.class_join_codes SET max_uses = 1, uses_count = 1 WHERE class_id = $1 AND revoked_at IS NULL`},
		"revoked":                              {domain.PreviewRevoked, `UPDATE app.class_join_codes SET revoked_at = now() WHERE class_id = $1 AND revoked_at IS NULL`},
		"self-join closed, as an unknown code": {domain.PreviewInvalid, `UPDATE app.classes SET self_join_enabled = false WHERE id = $1`},
	} {
		t.Run(name, func(t *testing.T) {
			classID, teacherID, _ := makeClassRow(t, pool)
			if _, err := pool.Exec(ctx, `UPDATE app.classes SET schedule_label = 'Thứ 3, 5 · 18:00', room = 'A1' WHERE id = $1`, classID); err != nil {
				t.Fatal(err)
			}
			code := issueCode(t, svc, classID, teacherID)
			if _, err := pool.Exec(ctx, refuse.sql, classID); err != nil {
				t.Fatal(err)
			}
			before := source.calls()
			got, err := svc.Queries.Preview.Handle(ctx, query.Preview{Code: code})
			if err != nil {
				t.Fatal(err)
			}
			if got != (domain.PreviewResult{Outcome: refuse.outcome}) {
				t.Errorf("the preview is %+v, want the refusal %v alone, as before R8", got, refuse.outcome)
			}
			if source.calls() != before {
				t.Errorf("a refused code read the class's series: %v", source.asked[before:])
			}
		})
	}
	unknown, err := svc.Queries.Preview.Handle(ctx, query.Preview{Code: "ZZZZ-ZZZZ"})
	if err != nil || unknown != (domain.PreviewResult{Outcome: domain.PreviewInvalid}) {
		t.Errorf("an unknown code previews as %+v (%v), want PreviewInvalid alone", unknown, err)
	}
}

func TestAStudentJoiningByCodeGetsTheClassWithItsWeeklyPattern(t *testing.T) {
	pool := newPool(t)
	svc := scheduledSvc(t, tuesdayThursdayIn("B2"))
	classID, teacherID, _ := makeClassRow(t, pool)
	code := issueCode(t, svc, classID, teacherID)
	_, _, student := makeClassRow(t, pool)

	joined, err := svc.Commands.EnrolExisting.Handle(context.Background(), command.EnrolExisting{UserID: student, Code: code})
	if err != nil {
		t.Fatal(err)
	}
	if joined.Outcome != domain.PreviewOK || len(joined.Class.Slots()) != 2 {
		t.Fatalf("the join answered %+v with slots %v, want the class's two weekly slots", joined, joined.Class.Slots())
	}
	if room := joined.Class.DerivedRoom(); room == nil || *room != "B2" {
		t.Errorf("the joined class meets in %v, want B2", room)
	}
}

func TestAStudentsClassesCarryTheirWeeklyPatternFromOneRead(t *testing.T) {
	pool := newPool(t)
	source := tuesdayThursdayIn("B2")
	svc := newSvc(t, pool).WithSchedules(source)
	first, teacherID, student := makeClassRow(t, pool)
	var second string
	if err := pool.QueryRow(context.Background(), `INSERT INTO app.classes (name, teacher_id, room) VALUES ('Lớp hai ' || $2::text, $1, 'A1') RETURNING id::text`, teacherID, nonce(t)).Scan(&second); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM app.class_members WHERE class_id = $1`, second)
		_, _ = pool.Exec(context.Background(), `DELETE FROM app.classes WHERE id = $1`, second)
	})
	if _, err := pool.Exec(context.Background(), `INSERT INTO app.class_members (class_id, user_id, joined_via, added_by) VALUES ($1, $2, 'admin', $3)`, second, student, teacherID); err != nil {
		t.Fatal(err)
	}

	mine, err := svc.Queries.ListMine.Handle(context.Background(), query.ListMine{UserID: student})
	if err != nil {
		t.Fatal(err)
	}
	if len(mine) != 2 {
		t.Fatalf("the student is in %d classes, want 2", len(mine))
	}
	rooms := map[string]string{}
	for _, c := range mine {
		if len(c.Timetable.Slots) != 2 {
			t.Errorf("class %s carries %d slots, want 2", c.ID, len(c.Timetable.Slots))
		}
		if room := c.DerivedRoom(); room != nil {
			rooms[c.ID] = *room
		}
	}
	if rooms[first] != "B2" || rooms[second] != "A1" {
		t.Errorf("the rooms are %v, want B2 (the series') for %s and A1 (its own) for %s", rooms, first, second)
	}
	if source.calls() != 1 || len(source.asked[0]) != 2 {
		t.Errorf("the series were asked for %v, want both classes in one read", source.asked)
	}
}
