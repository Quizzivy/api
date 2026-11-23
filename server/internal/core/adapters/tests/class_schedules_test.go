//go:build integration

package adapters_test

import (
	"bytes"
	"context"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"quizzivy/internal/core/adapters"
	classesapp "quizzivy/internal/modules/classes/application"
	classescmd "quizzivy/internal/modules/classes/application/command"
	classesquery "quizzivy/internal/modules/classes/application/query"
	classesdomain "quizzivy/internal/modules/classes/domain"
	classesrepo "quizzivy/internal/modules/classes/repositories"
	timetableapp "quizzivy/internal/modules/timetable/application"
	timetablerepo "quizzivy/internal/modules/timetable/repositories"
	"quizzivy/internal/platform/db"
	"quizzivy/internal/shared/access"
)

// The classes module derives every class surface's schedule and room from
// the timetable's active series through adapters.ClassSchedules (T-R8.7).
// These run the real classes, the real adapter and the real timetable store
// together.

type countingQueries struct {
	mu sync.Mutex
	n  int
}

func (c *countingQueries) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.n++
	return ctx
}

func (c *countingQueries) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func (c *countingQueries) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n
}

type scheduleWorld struct {
	pool    *pgxpool.Pool
	queries *countingQueries
	classes *classesapp.Application
	users   []string
}

func newScheduleWorld(t *testing.T) *scheduleWorld {
	t.Helper()
	cfg, err := pgxpool.ParseConfig(db.TestDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	w := &scheduleWorld{queries: &countingQueries{}}
	cfg.ConnConfig.Tracer = w.queries
	if w.pool, err = pgxpool.NewWithConfig(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(w.pool.Close)
	keys, err := classesdomain.NewJoinCodeKeys(bytes.Repeat([]byte{0x5c}, classesdomain.JoinCodeKeySize), nil)
	if err != nil {
		t.Fatal(err)
	}
	dbx := db.NewContext(w.pool)
	timetable := timetableapp.New(timetablerepo.NewPostgres(dbx))
	w.classes = classesapp.New(classesrepo.NewPostgres(dbx), nil, keys).
		WithSchedules(adapters.ClassSchedules{Query: timetable.Queries.ActiveSeries})
	t.Cleanup(func() {
		ctx := context.Background()
		for _, sql := range []string{
			`DELETE FROM app.audit_log WHERE actor_user_id = ANY($1::uuid[])`,
			`DELETE FROM app.class_session_series WHERE created_by = ANY($1::uuid[])`,
			`DELETE FROM app.class_members WHERE user_id = ANY($1::uuid[])`,
			`DELETE FROM app.class_join_codes WHERE class_id IN (SELECT id FROM app.classes WHERE teacher_id = ANY($1::uuid[]))`,
			`DELETE FROM app.classes WHERE teacher_id = ANY($1::uuid[])`,
			`DELETE FROM app.users WHERE id = ANY($1::uuid[])`,
		} {
			if _, err := w.pool.Exec(ctx, sql, w.users); err != nil {
				t.Errorf("cleanup %q: %v", sql, err)
			}
		}
	})
	return w
}

func (w *scheduleWorld) user(t *testing.T, builtin string) string {
	t.Helper()
	var id string
	if err := w.pool.QueryRow(context.Background(),
		`INSERT INTO app.users (email, full_name, role_id) VALUES ($1, 'Người dùng', (SELECT id FROM app.roles WHERE builtin_key = $2)) RETURNING id::text`,
		builtin+"-"+uuid.NewString()+"@example.com", builtin).Scan(&id); err != nil {
		t.Fatal(err)
	}
	w.users = append(w.users, id)
	return id
}

func (w *scheduleWorld) class(t *testing.T, teacher, name string, label, room *string) string {
	t.Helper()
	var id string
	if err := w.pool.QueryRow(context.Background(),
		`INSERT INTO app.classes (name, teacher_id, schedule_label, room) VALUES ($1, $2, $3, $4) RETURNING id::text`,
		name, teacher, label, room).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func (w *scheduleWorld) member(t *testing.T, classID, student, teacher string) {
	t.Helper()
	if _, err := w.pool.Exec(context.Background(),
		`INSERT INTO app.class_members (class_id, user_id, joined_via, added_by) VALUES ($1, $2, 'admin', $3)`, classID, student, teacher); err != nil {
		t.Fatal(err)
	}
}

// series stores a weekly series of classID that began startedDaysAgo days
// ago (negative: begins later) and ends endsInDays from today (nil: never),
// in the centre's zone.
func (w *scheduleWorld) series(t *testing.T, classID, teacher string, weekday int, start string, minutes int, room *string, startedDaysAgo int, endsInDays *int) {
	t.Helper()
	today := time.Now().In(zoneHCM(t))
	startsOn := today.AddDate(0, 0, -startedDaysAgo).Format("2006-01-02")
	var endsOn *string
	if endsInDays != nil {
		d := today.AddDate(0, 0, *endsInDays).Format("2006-01-02")
		endsOn = &d
	}
	if _, err := w.pool.Exec(context.Background(), `
		INSERT INTO app.class_session_series (class_id, weekday, start_time, duration_minutes, room, starts_on, ends_on, time_zone, created_by)
		VALUES ($1, $2, $3::time, $4, $5, $6::date, $7::date, 'Asia/Ho_Chi_Minh', $8)`,
		classID, weekday, start, minutes, room, startsOn, endsOn, teacher); err != nil {
		t.Fatal(err)
	}
}

func zoneHCM(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Asia/Ho_Chi_Minh")
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func days(n int) *int       { return &n }
func text(s string) *string { return &s }

func deref(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}

func slotsOf(t classesdomain.Timetable) []classesdomain.ScheduleSlot { return t.Slots }

func TestEveryClassSurfaceDerivesTheScheduleFromTheSeriesActiveTodayOrLater(t *testing.T) {
	w := newScheduleWorld(t)
	ctx := context.Background()
	teacher, student := w.user(t, "teacher"), w.user(t, "student")
	name := "Lớp lịch " + uuid.NewString()
	classID := w.class(t, teacher, name, text("Thứ 2, 4 · 18:00"), nil)
	w.member(t, classID, student, teacher)

	w.series(t, classID, teacher, 1, "18:00", 90, text("B2"), 60, nil)       // Monday, open-ended
	w.series(t, classID, teacher, 1, "18:00", 90, text("B2"), 10, days(90))  // a second series at Monday 18:00
	w.series(t, classID, teacher, 3, "18:00", 90, text("B2"), 60, days(0))   // Wednesday, its last day today
	w.series(t, classID, teacher, 5, "08:00", 60, text("C3"), 90, days(-7))  // Friday, ended last week
	w.series(t, classID, teacher, 6, "09:00", 60, text("B2"), -30, days(90)) // Saturday, from next month
	want := []classesdomain.ScheduleSlot{
		{Weekday: 1, StartTime: "18:00", DurationMinutes: 90},
		{Weekday: 3, StartTime: "18:00", DurationMinutes: 90},
		{Weekday: 6, StartTime: "09:00", DurationMinutes: 60},
	}
	check := func(surface string, slots []classesdomain.ScheduleSlot, room *string) {
		t.Helper()
		if !reflect.DeepEqual(slots, want) {
			t.Errorf("%s: the slots are %+v, want %+v: the ended Friday left out, the two Monday series one slot", surface, slots, want)
		}
		if deref(room) != "B2" {
			t.Errorf("%s: the room is %s, want B2, the one room every active series shares (the ended C3 does not count)", surface, deref(room))
		}
	}

	got, err := w.classes.Queries.Get.Handle(ctx, classesquery.Get{ClassID: classID, Scope: access.Scope{UserID: teacher}})
	if err != nil {
		t.Fatal(err)
	}
	check("getClass", slotsOf(got.Timetable), got.DerivedRoom())
	if got.Room != nil || deref(got.ScheduleLabel) != "Thứ 2, 4 · 18:00" {
		t.Errorf("getClass: the own room is %s and the label %s, want none and the stored label", deref(got.Room), deref(got.ScheduleLabel))
	}

	listed, err := w.classes.Queries.List.Handle(ctx, classesquery.List{Input: classesdomain.ListInput{Scope: access.Scope{UserID: teacher}, Limit: 10}})
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Items) != 1 {
		t.Fatalf("listClasses: %d rows, want the teacher's one class", len(listed.Items))
	}
	check("listClasses", slotsOf(listed.Items[0].Timetable), listed.Items[0].DerivedRoom())

	mine, err := w.classes.Queries.ListMine.Handle(ctx, classesquery.ListMine{UserID: student})
	if err != nil {
		t.Fatal(err)
	}
	if len(mine) != 1 {
		t.Fatalf("listMyClasses: %d classes, want 1", len(mine))
	}
	check("listMyClasses", slotsOf(mine[0].Timetable), mine[0].DerivedRoom())

	all, err := w.classes.Queries.ListAll.Handle(ctx, classesquery.ListAll{Input: classesdomain.AdminListInput{Query: name, Limit: 10}})
	if err != nil {
		t.Fatal(err)
	}
	if len(all.Items) != 1 {
		t.Fatalf("listAllClasses: %d rows for %q, want 1", len(all.Items), name)
	}
	check("listAllClasses", slotsOf(all.Items[0].Timetable), all.Items[0].DerivedRoom())

	rotated, err := w.classes.Commands.Rotate.Handle(ctx, classescmd.Rotate{Request: classesdomain.RotateRequest{ClassID: classID, ActorUserID: teacher}})
	if err != nil {
		t.Fatal(err)
	}
	preview, err := w.classes.Queries.Preview.Handle(ctx, classesquery.Preview{Code: rotated.Code})
	if err != nil {
		t.Fatal(err)
	}
	if preview.Schedule == nil {
		t.Fatalf("the preview has no schedule: %+v", preview)
	}
	check("previewJoinCode", preview.Schedule.Slots, preview.Room)
	if deref(preview.Schedule.Label) != "Thứ 2, 4 · 18:00" {
		t.Errorf("previewJoinCode: the label is %s, want the class's own", deref(preview.Schedule.Label))
	}
}

func TestAClassWithNoSeriesHasItsLabelAloneAndItsOwnRoom(t *testing.T) {
	w := newScheduleWorld(t)
	teacher := w.user(t, "teacher")
	classID := w.class(t, teacher, "Lớp nhãn "+uuid.NewString(), text("Thứ 7 · 8:00"), text("A1"))
	ended := w.class(t, teacher, "Lớp đã dừng "+uuid.NewString(), nil, nil)
	w.series(t, ended, teacher, 2, "18:00", 90, text("B2"), 120, days(-1))

	for id, want := range map[string]string{classID: "A1", ended: "<nil>"} {
		got, err := w.classes.Queries.Get.Handle(context.Background(), classesquery.Get{ClassID: id, Scope: access.Scope{UserID: teacher}})
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Timetable.Slots) != 0 || deref(got.DerivedRoom()) != want {
			t.Errorf("class %s: slots %+v and room %s, want no slots and the room %s", id, got.Timetable.Slots, deref(got.DerivedRoom()), want)
		}
	}
}

func TestTheRoomIsTheClasssOwnElseTheOneAllActiveSeriesShareElseNone(t *testing.T) {
	w := newScheduleWorld(t)
	teacher := w.user(t, "teacher")
	two := w.class(t, teacher, "Lớp hai phòng "+uuid.NewString(), nil, nil)
	w.series(t, two, teacher, 1, "18:00", 90, text("A1"), 7, nil)
	w.series(t, two, teacher, 3, "18:00", 90, text("B2"), 7, nil)
	unnamed := w.class(t, teacher, "Lớp thiếu phòng "+uuid.NewString(), nil, nil)
	w.series(t, unnamed, teacher, 1, "18:00", 90, text("B2"), 7, nil)
	w.series(t, unnamed, teacher, 3, "18:00", 90, nil, 7, nil)
	own := w.class(t, teacher, "Lớp có phòng "+uuid.NewString(), nil, text("C3"))
	w.series(t, own, teacher, 1, "18:00", 90, text("B2"), 7, nil)

	for id, want := range map[string]string{two: "<nil>", unnamed: "<nil>", own: "C3"} {
		got, err := w.classes.Queries.Get.Handle(context.Background(), classesquery.Get{ClassID: id, Scope: access.Scope{UserID: teacher}})
		if err != nil {
			t.Fatal(err)
		}
		if deref(got.DerivedRoom()) != want || len(got.Timetable.Slots) == 0 {
			t.Errorf("class %s: the room is %s with slots %+v, want %s", id, deref(got.DerivedRoom()), got.Timetable.Slots, want)
		}
	}
}

// TestTheListsReadEverySeriesInOneQuery pins that a list page costs the same
// number of queries for twelve scheduled classes as for one: the series are
// read for the whole page at once, never per class.
func TestTheListsReadEverySeriesInOneQuery(t *testing.T) {
	w := newScheduleWorld(t)
	ctx := context.Background()
	few, many := w.user(t, "teacher"), w.user(t, "teacher")
	oneStudent, busyStudent := w.user(t, "student"), w.user(t, "student")
	tag := uuid.NewString()
	scheduled := func(teacher, name string, student string) {
		id := w.class(t, teacher, name, nil, nil)
		w.series(t, id, teacher, 2, "18:00", 90, text("B2"), 7, nil)
		w.series(t, id, teacher, 4, "18:00", 90, text("B2"), 7, nil)
		w.member(t, id, student, teacher)
	}
	scheduled(few, "Lớp ít "+tag, oneStudent)
	for i := range 12 {
		scheduled(many, fmt.Sprintf("Lớp nhiều %s %02d", tag, i), busyStudent)
	}

	cost := func(read func() int) (queries, rows int) {
		t.Helper()
		before := w.queries.count()
		rows = read()
		return w.queries.count() - before, rows
	}
	list := func(teacher string) func() int {
		return func() int {
			got, err := w.classes.Queries.List.Handle(ctx, classesquery.List{Input: classesdomain.ListInput{Scope: access.Scope{UserID: teacher}, Limit: 50}})
			if err != nil {
				t.Fatal(err)
			}
			for _, item := range got.Items {
				if len(item.Timetable.Slots) != 2 {
					t.Errorf("class %s lists %d slots, want 2", item.ID, len(item.Timetable.Slots))
				}
			}
			return len(got.Items)
		}
	}
	mine := func(student string) func() int {
		return func() int {
			got, err := w.classes.Queries.ListMine.Handle(ctx, classesquery.ListMine{UserID: student})
			if err != nil {
				t.Fatal(err)
			}
			for _, c := range got {
				if len(c.Timetable.Slots) != 2 {
					t.Errorf("class %s lists %d slots, want 2", c.ID, len(c.Timetable.Slots))
				}
			}
			return len(got)
		}
	}
	admin := func(query string) func() int {
		return func() int {
			got, err := w.classes.Queries.ListAll.Handle(ctx, classesquery.ListAll{Input: classesdomain.AdminListInput{Query: query, Limit: 50}})
			if err != nil {
				t.Fatal(err)
			}
			return len(got.Items)
		}
	}
	for name, pair := range map[string][2]func() int{
		"listClasses":    {list(few), list(many)},
		"listMyClasses":  {mine(oneStudent), mine(busyStudent)},
		"listAllClasses": {admin("Lớp ít " + tag), admin("Lớp nhiều " + tag)},
	} {
		one, oneRows := cost(pair[0])
		twelve, twelveRows := cost(pair[1])
		if oneRows != 1 || twelveRows != 12 {
			t.Fatalf("%s: %d and %d rows, want 1 and 12", name, oneRows, twelveRows)
		}
		if one != twelve {
			t.Errorf("%s: one scheduled class costs %d queries and twelve cost %d, want the same: the series are read once per page", name, one, twelve)
		}
	}
}
