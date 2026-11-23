package domain

import (
	"cmp"
	"slices"
)

// MaxScheduleSlots is the most weekly meeting times a class's schedule lists
// (ClassSchedule.slots maxItems): two a day, every day.
const MaxScheduleSlots = 14

// ScheduleSlot is one weekly meeting time: an ISO weekday (Monday 1 to
// Sunday 7), a wall-clock start "HH:MM" read in the zone of the series it
// comes from, and a length. It holds no date, note or cancellation.
type ScheduleSlot struct {
	Weekday         int
	StartTime       string
	DurationMinutes int
}

// WeeklySeries is one of a class's active weekly series as its schedule reads
// it: when it meets and its room, nil for none.
type WeeklySeries struct {
	ScheduleSlot
	Room *string
}

// Timetable is what a class's active weekly series say about when and where
// it meets (T-R8.7): Slots, one per weekday and start time, ordered by
// weekday, then start time, at most MaxScheduleSlots; and SharedRoom, the one
// room every active series names, nil when there is no series, one names
// none, or two name different rooms. The zero Timetable is a class with no
// active series.
type Timetable struct {
	Slots      []ScheduleSlot
	SharedRoom *string
}

// TimetableOf derives a class's Timetable from its active series. Two series
// at one weekday and start time are one slot, the longer of their lengths, so
// a schedule line never names one time twice; the first MaxScheduleSlots
// slots in order are kept.
func TimetableOf(series []WeeklySeries) Timetable {
	if len(series) == 0 {
		return Timetable{}
	}
	slots := make([]ScheduleSlot, 0, len(series))
	for _, s := range series {
		slots = append(slots, s.ScheduleSlot)
	}
	slices.SortFunc(slots, func(a, b ScheduleSlot) int {
		return cmp.Or(cmp.Compare(a.Weekday, b.Weekday), cmp.Compare(a.StartTime, b.StartTime), cmp.Compare(b.DurationMinutes, a.DurationMinutes))
	})
	slots = slices.CompactFunc(slots, func(a, b ScheduleSlot) bool {
		return a.Weekday == b.Weekday && a.StartTime == b.StartTime
	})
	if len(slots) > MaxScheduleSlots {
		slots = slots[:MaxScheduleSlots]
	}
	return Timetable{Slots: slots, SharedRoom: sharedRoom(series)}
}

func sharedRoom(series []WeeklySeries) *string {
	room := series[0].Room
	for _, s := range series {
		if s.Room == nil || room == nil || *s.Room != *room {
			return nil
		}
	}
	return room
}

// Schedule is a class's schedule as the preview shows it: the slots of its
// active series and its own free-text label.
type Schedule struct {
	Slots []ScheduleSlot
	Label *string
}

// ScheduleOf is the schedule of a class with label and t, nil when it has
// neither slots nor a label.
func ScheduleOf(label *string, t Timetable) *Schedule {
	if len(t.Slots) == 0 && label == nil {
		return nil
	}
	return &Schedule{Slots: t.Slots, Label: label}
}

// RoomOf is where a class meets (Class.room): its own room, else the one
// room all its active series share, else nil.
func RoomOf(own *string, t Timetable) *string {
	if own != nil {
		return own
	}
	return t.SharedRoom
}
