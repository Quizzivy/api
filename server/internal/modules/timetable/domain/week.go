package domain

import (
	"slices"
	"strings"
	"time"
)

// WeekDays is how many days a student's week holds, Monday to Sunday.
const WeekDays = 7

// DueAssignment is an assignment that targets a student, as the assignments
// module's student query reads it for them (adapters.TimetableDueItems):
// ClosesAt is their own close, their override included and an early close
// applied; HandedIn is whether a finished attempt counts against them; Live
// whether one is under way before its deadline; DurationMinutes their time
// limit, nil when none applies.
type DueAssignment struct {
	ID              string
	Title           string
	ClosesAt        time.Time
	HandedIn        bool
	Live            bool
	DurationMinutes *int
}

// Due reports whether d is due for its student: not handed in, or handed in
// once and taken again with an attempt under way.
func (d DueAssignment) Due() bool { return d.Live || !d.HandedIn }

// WeekItem is one item of a student's day: a session, or an assignment due.
type WeekItem struct {
	Session *Session
	Due     *DueAssignment
}

// At is when the item falls: a session's start, a due item's close.
func (i WeekItem) At() time.Time {
	if i.Session != nil {
		return i.Session.StartsAt
	}
	return i.Due.ClosesAt
}

func (i WeekItem) id() string {
	if i.Session != nil {
		return i.Session.ID.String()
	}
	return i.Due.ID
}

// WeekDay is one date of a student's week and what falls on it.
type WeekDay struct {
	Date  time.Time
	Items []WeekItem
}

// Week is a student's week: Monday Start to Sunday End, both dates, and
// WeekDays days, Monday first.
type Week struct {
	Start, End time.Time
	Days       []WeekDay
}

// MondayOf is the Monday of the ISO week that holds day, as a date.
func MondayOf(day time.Time) time.Time {
	day = Day(day)
	return day.AddDate(0, 0, 1-int(WeekdayOf(day)))
}

// WeekRange is the instants the week starting on monday covers in loc: its
// Monday's midnight to the next Monday's.
func WeekRange(monday time.Time, loc *time.Location) (time.Time, time.Time) {
	midnight := Clock{}
	return midnight.On(monday, loc), midnight.On(monday.AddDate(0, 0, WeekDays), loc)
}

// StudentWeek lays sessions and due assignments out over the week starting on
// monday, each on the day it falls on in loc: a session by its start, a due
// assignment by its close. A session outside the week, and an assignment that
// closes outside it or is not Due, are left out; a cancelled session stays.
// Each day is ordered by time, a session before a due item at the same
// instant, then by id.
func StudentWeek(monday time.Time, loc *time.Location, sessions []Session, due []DueAssignment) Week {
	monday = Day(monday)
	from, to := WeekRange(monday, loc)
	out := Week{Start: monday, End: monday.AddDate(0, 0, WeekDays-1), Days: make([]WeekDay, WeekDays)}
	index := make(map[string]int, WeekDays)
	for i := range out.Days {
		out.Days[i] = WeekDay{Date: monday.AddDate(0, 0, i), Items: []WeekItem{}}
		index[out.Days[i].Date.Format(time.DateOnly)] = i
	}
	place := func(item WeekItem) {
		at := item.At()
		if !in(at, from, to) {
			return
		}
		if i, ok := index[Day(at.In(loc)).Format(time.DateOnly)]; ok {
			out.Days[i].Items = append(out.Days[i].Items, item)
		}
	}
	for i := range sessions {
		place(WeekItem{Session: &sessions[i]})
	}
	for i := range due {
		if due[i].Due() {
			place(WeekItem{Due: &due[i]})
		}
	}
	for i := range out.Days {
		slices.SortFunc(out.Days[i].Items, compareItems)
	}
	return out
}

// compareItems orders a day's items by time, a session before a due item at
// the same instant, then by id.
func compareItems(a, b WeekItem) int {
	if c := a.At().Compare(b.At()); c != 0 {
		return c
	}
	if (a.Session == nil) != (b.Session == nil) {
		if a.Session != nil {
			return -1
		}
		return 1
	}
	return strings.Compare(a.id(), b.id())
}
