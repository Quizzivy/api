package http_test

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"quizzivy/gen/openapi"
	"quizzivy/internal/modules/classes/application"
	"quizzivy/internal/modules/classes/application/query"
	"quizzivy/internal/modules/classes/domain"
	classeshttp "quizzivy/internal/modules/classes/http"
	"quizzivy/internal/shared/access"
	"quizzivy/internal/shared/cqrs"
)

// A class with weekly series: Tuesday and Thursday at 18:00 in room B2, and no
// room of its own.
func scheduled() domain.Timetable {
	room := "B2"
	return domain.Timetable{
		Slots: []domain.ScheduleSlot{
			{Weekday: 2, StartTime: "18:00", DurationMinutes: 90},
			{Weekday: 4, StartTime: "18:00", DurationMinutes: 90},
		},
		SharedRoom: &room,
	}
}

func wantSlots(t *testing.T, who string, got openapi.ClassSchedule, label *string) {
	t.Helper()
	want := []openapi.ScheduleSlot{{Weekday: 2, StartTime: "18:00", DurationMinutes: 90}, {Weekday: 4, StartTime: "18:00", DurationMinutes: 90}}
	if !slices.Equal(got.Slots, want) || got.Label != label {
		t.Errorf("%s: the schedule is %+v, want the series' two slots and the label %v", who, got, label)
	}
}

func TestEveryTeacherSurfaceDerivesTheRoomAndKeepsTheOwnRoomStored(t *testing.T) {
	label := "Thứ 3, 5 · 18:00"
	class := domain.Class{ID: uuid.NewString(), Name: "Lớp", ScheduleLabel: &label, Timetable: scheduled(), CreatedAt: time.Now()}
	app := classeshttp.NewClasses(&application.Application{Queries: application.Queries{
		Get: cqrs.HandlerFunc[query.Get, domain.Class](func(context.Context, query.Get) (domain.Class, error) { return class, nil }),
		List: cqrs.HandlerFunc[query.List, query.ListResult](func(context.Context, query.List) (query.ListResult, error) {
			return query.ListResult{Items: []domain.ListedClass{{Class: class}}}, nil
		}),
		Facets: cqrs.HandlerFunc[query.Facets, domain.Facets](func(context.Context, query.Facets) (domain.Facets, error) { return domain.Facets{}, nil }),
		ListAll: cqrs.HandlerFunc[query.ListAll, query.ListAllResult](func(context.Context, query.ListAll) (query.ListAllResult, error) {
			return query.ListAllResult{Items: []domain.AdminClass{{Class: class, Teacher: domain.ClassTeacher{ID: uuid.NewString(), Name: "Cô Thương"}}}}, nil
		}),
	}})
	ctx := contextAs(t, access.Principal{UserID: uuid.NewString(), Permissions: access.NewSet(access.TeachingClassesWrite)})

	got, err := app.GetClass(ctx, openapi.GetClassRequestObject{Id: uuid.New()})
	if err != nil {
		t.Fatal(err)
	}
	read := got.(openapi.GetClass200JSONResponse)
	wantSlots(t, "getClass", read.Schedule, &label)
	if read.Room == nil || *read.Room != "B2" || read.OwnRoom != nil {
		t.Errorf("getClass: room %v and ownRoom %v, want the series' B2 derived and no own room", read.Room, read.OwnRoom)
	}

	listed, err := app.ListClasses(ctx, openapi.ListClassesRequestObject{})
	if err != nil {
		t.Fatal(err)
	}
	row := listed.(openapi.ListClasses200JSONResponse).Body.Items[0]
	wantSlots(t, "listClasses", row.Schedule, &label)
	if row.Room == nil || *row.Room != "B2" || row.OwnRoom != nil {
		t.Errorf("listClasses: room %v and ownRoom %v, want B2 derived and no own room", row.Room, row.OwnRoom)
	}

	own := "A1"
	class.Room = &own
	got, err = app.GetClass(ctx, openapi.GetClassRequestObject{Id: uuid.New()})
	if err != nil {
		t.Fatal(err)
	}
	if read := got.(openapi.GetClass200JSONResponse); read.Room == nil || *read.Room != "A1" || read.OwnRoom == nil || *read.OwnRoom != "A1" {
		t.Errorf("a class with its own room: room %v and ownRoom %v, want A1 for both", read.Room, read.OwnRoom)
	}

	class.Room = nil
	all, err := app.ListAllClasses(ctx, openapi.ListAllClassesRequestObject{})
	if err != nil {
		t.Fatal(err)
	}
	admin := all.(openapi.ListAllClasses200JSONResponse).Body.Items[0]
	wantSlots(t, "listAllClasses", admin.Schedule, &label)
	if admin.Room == nil || *admin.Room != "B2" {
		t.Errorf("listAllClasses: room %v, want B2 derived", admin.Room)
	}
}

func TestAStudentsScheduledClassCarriesTheWeeklyPatternAndNoCountDateOrNote(t *testing.T) {
	teacher := "Cô Thương"
	app := classeshttp.NewClasses(&application.Application{Queries: application.Queries{
		ListMine: cqrs.HandlerFunc[query.ListMine, []domain.MyClass](func(context.Context, query.ListMine) ([]domain.MyClass, error) {
			return []domain.MyClass{{ID: uuid.NewString(), Name: "Lớp", TeacherID: uuid.NewString(), TeacherName: &teacher, Timetable: scheduled(), JoinedAt: time.Now()}}, nil
		}),
	}})
	ctx := contextAs(t, access.Principal{UserID: uuid.NewString(), Permissions: access.NewSet(access.TeachingClassesWrite)})
	response, err := app.ListMyClasses(ctx, openapi.ListMyClassesRequestObject{})
	if err != nil {
		t.Fatal(err)
	}
	item := response.(openapi.ListMyClasses200JSONResponse).Items[0]
	if got, want := keysOf(t, item), []string{"description", "id", "joinedAt", "name", "room", "schedule", "scheduleLabel", "teacherAvatarUrl", "teacherId", "teacherName"}; !slices.Equal(got, want) {
		t.Errorf("a scheduled class carries %v, want exactly %v: no count, roster, code or average", got, want)
	}
	wantSlots(t, "listMyClasses", item.Schedule, nil)
	if item.Room == nil || *item.Room != "B2" {
		t.Errorf("listMyClasses: room %v, want the series' B2", item.Room)
	}
	raw, err := json.Marshal(item.Schedule)
	if err != nil {
		t.Fatal(err)
	}
	var schedule map[string]any
	if err := json.Unmarshal(raw, &schedule); err != nil {
		t.Fatal(err)
	}
	if got := keysOf(t, schedule); !slices.Equal(got, []string{"label", "slots"}) {
		t.Errorf("a student's schedule carries %v, want exactly label and slots: no dates, notes or cancellations", got)
	}
}

func TestTheJoinedClassDerivesItsRoomOnceItsSeriesAreRead(t *testing.T) {
	timetable := scheduled()
	got := classeshttp.ToAPIClass(domain.EnrolledClass{ID: uuid.NewString(), Name: "Lớp", Timetable: &timetable, CreatedAt: time.Now()})
	wantSlots(t, "the joined class", got.Schedule, nil)
	if got.Room == nil || *got.Room != "B2" || got.OwnRoom != nil {
		t.Errorf("the joined class: room %v and ownRoom %v, want B2 and no ownRoom on a student's response", got.Room, got.OwnRoom)
	}
}
