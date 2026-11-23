package domain_test

import (
	"fmt"
	"reflect"
	"testing"

	"quizzivy/internal/modules/classes/domain"
)

func weekly(weekday int, start string, minutes int, room *string) domain.WeeklySeries {
	return domain.WeeklySeries{ScheduleSlot: domain.ScheduleSlot{Weekday: weekday, StartTime: start, DurationMinutes: minutes}, Room: room}
}

func slot(weekday int, start string, minutes int) domain.ScheduleSlot {
	return domain.ScheduleSlot{Weekday: weekday, StartTime: start, DurationMinutes: minutes}
}

func roomName(s string) *string { return &s }

func TestAClassWithNoSeriesHasItsLabelAloneAndItsOwnRoom(t *testing.T) {
	got := domain.TimetableOf(nil)
	if len(got.Slots) != 0 || got.SharedRoom != nil {
		t.Errorf("no series derive %+v, want no slots and no room", got)
	}
	label := "Thứ 3, 5 · 18:00"
	if s := domain.ScheduleOf(&label, got); s == nil || len(s.Slots) != 0 || s.Label == nil || *s.Label != label {
		t.Errorf("the schedule of a class with a label and no series is %+v, want the label alone", s)
	}
	if s := domain.ScheduleOf(nil, got); s != nil {
		t.Errorf("a class with neither slots nor a label has the schedule %+v, want none (the preview's null)", s)
	}
	if room := domain.RoomOf(roomName("A1"), got); room == nil || *room != "A1" {
		t.Errorf("a class with no series meets in %v, want its own room A1", room)
	}
	if room := domain.RoomOf(nil, got); room != nil {
		t.Errorf("a class with no room and no series meets in %q, want none", *room)
	}
}

func TestTheSlotsAreOrderedByWeekdayThenTimeAndTwoSeriesAtOneTimeAreOneSlot(t *testing.T) {
	got := domain.TimetableOf([]domain.WeeklySeries{
		weekly(5, "18:00", 90, nil),
		weekly(1, "18:00", 90, nil),
		weekly(3, "08:30", 60, nil),
		weekly(3, "18:00", 90, nil),
		weekly(1, "18:00", 90, nil),  // a second series at Monday 18:00
		weekly(1, "07:45", 45, nil),  // earlier the same day
		weekly(5, "18:00", 120, nil), // Friday 18:00 again, longer
	})
	want := []domain.ScheduleSlot{
		slot(1, "07:45", 45), slot(1, "18:00", 90),
		slot(3, "08:30", 60), slot(3, "18:00", 90),
		slot(5, "18:00", 120),
	}
	if !reflect.DeepEqual(got.Slots, want) {
		t.Errorf("the slots are %+v, want %+v: one slot per weekday and start time, the longer length kept", got.Slots, want)
	}
}

func TestTheScheduleListsAtMostFourteenSlots(t *testing.T) {
	var series []domain.WeeklySeries
	for weekday := 7; weekday >= 1; weekday-- {
		for _, start := range []string{"20:00", "17:00", "08:00"} {
			series = append(series, weekly(weekday, start, 60, nil))
		}
	}
	got := domain.TimetableOf(series)
	if len(got.Slots) != domain.MaxScheduleSlots {
		t.Fatalf("21 distinct series give %d slots, want %d", len(got.Slots), domain.MaxScheduleSlots)
	}
	if first, last := got.Slots[0], got.Slots[len(got.Slots)-1]; first != slot(1, "08:00", 60) || last != slot(5, "17:00", 60) {
		t.Errorf("the slots run %+v to %+v, want the first fourteen in order, Monday 08:00 to Friday 17:00", first, last)
	}
}

func TestTheRoomIsTheClasssOwnElseTheOneEverySeriesSharesElseNone(t *testing.T) {
	a1, b2 := roomName("A1"), roomName("B2")
	for name, c := range map[string]struct {
		own    *string
		series []domain.WeeklySeries
		want   *string
	}{
		"the class's own room wins over the series'": {a1, []domain.WeeklySeries{weekly(1, "18:00", 90, b2), weekly(3, "18:00", 90, b2)}, a1},
		"the own room stands when series differ":     {a1, []domain.WeeklySeries{weekly(1, "18:00", 90, a1), weekly(3, "18:00", 90, b2)}, a1},
		"no own room, one room every series shares":  {nil, []domain.WeeklySeries{weekly(1, "18:00", 90, b2), weekly(3, "18:00", 90, roomName("B2"))}, b2},
		"no own room, a single series with a room":   {nil, []domain.WeeklySeries{weekly(2, "18:00", 90, b2)}, b2},
		"no own room, two rooms":                     {nil, []domain.WeeklySeries{weekly(1, "18:00", 90, a1), weekly(3, "18:00", 90, b2)}, nil},
		"no own room, one series names none":         {nil, []domain.WeeklySeries{weekly(1, "18:00", 90, b2), weekly(3, "18:00", 90, nil)}, nil},
		"no own room, the first series names none":   {nil, []domain.WeeklySeries{weekly(1, "18:00", 90, nil), weekly(3, "18:00", 90, b2)}, nil},
		"no own room, no series":                     {nil, nil, nil},
	} {
		got := domain.RoomOf(c.own, domain.TimetableOf(c.series))
		if fmt.Sprint(deref(got)) != fmt.Sprint(deref(c.want)) {
			t.Errorf("%s: the room is %v, want %v", name, deref(got), deref(c.want))
		}
	}
}

func deref(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}

func TestAClassReadsItsDerivedRoom(t *testing.T) {
	shared := domain.TimetableOf([]domain.WeeklySeries{weekly(1, "18:00", 90, roomName("B2"))})
	class := domain.Class{Timetable: shared}
	if room := class.DerivedRoom(); room == nil || *room != "B2" {
		t.Errorf("a class with no own room derives %v, want its series' B2", deref(room))
	}
	class.Room = roomName("A1")
	if room := class.DerivedRoom(); room == nil || *room != "A1" {
		t.Errorf("a class with its own room derives %v, want A1", deref(room))
	}
	mine := domain.MyClass{Timetable: shared}
	if room := mine.DerivedRoom(); room == nil || *room != "B2" {
		t.Errorf("a student's class derives %v, want B2", deref(room))
	}
	joined := domain.EnrolledClass{Room: roomName("A1")}
	if room := joined.DerivedRoom(); room == nil || *room != "A1" || joined.Slots() != nil {
		t.Errorf("a joined class whose series were not read derives %v with slots %v, want its own room and none", deref(room), joined.Slots())
	}
	joined = domain.EnrolledClass{Timetable: &shared}
	if room := joined.DerivedRoom(); room == nil || *room != "B2" || len(joined.Slots()) != 1 {
		t.Errorf("a joined class derives %v with slots %v, want B2 and its one slot", deref(room), joined.Slots())
	}
}
