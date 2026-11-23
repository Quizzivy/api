// Package domain is the timetable's model (DG-92): a class's weekly series,
// the rows that store a one-off session, a change to one date of a series or
// an occurrence with attendance, the opaque session id that names either, and
// the OccurrenceManager that computes the sessions of a range from series and
// rows without writing anything.
package domain

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"quizzivy/internal/shared/content"
	"quizzivy/internal/shared/validation"
)

// DefaultZone is the zone a series keeps when its teacher stored none: the
// centre's.
const DefaultZone = "Asia/Ho_Chi_Minh"

// MaxSeriesDays is the most days a series' last date may follow its first
// (class_session_series_two_years), and MaxRoom and MaxNote the most
// characters a room and a note hold, as the contract and the tables bound them.
const (
	MaxSeriesDays = 731
	MaxRoom       = 60
	MaxNote       = 200
)

// The errors every command and read answers with. ErrNotFound covers a class,
// session or series that is missing, malformed or outside the caller's scope,
// so another teacher's answers exactly as a missing one does.
var (
	ErrNotFound      = errors.New("timetable: no such class, session or series")
	ErrClassArchived = errors.New("timetable: the class is archived")
	ErrInSeries      = errors.New("timetable: the session is an occurrence of a weekly series")
	ErrHasAttendance = errors.New("timetable: a student is marked on the session")
)

// Day is the calendar day t falls on in its own location, as midnight UTC: the
// form a DATE column reads into and every date of this package is held in.
func Day(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// Today is the calendar day it is at now in loc.
func Today(now time.Time, loc *time.Location) time.Time { return Day(now.In(loc)) }

// LocationOf loads zone, or DefaultZone when zone is empty or names no zone
// this server has, or DefaultZone's fixed offset on a host without zone data.
func LocationOf(zone string) *time.Location {
	if zone != "" && zone != "Local" {
		if loc, err := time.LoadLocation(zone); err == nil {
			return loc
		}
	}
	if loc, err := time.LoadLocation(DefaultZone); err == nil {
		return loc
	}
	return time.FixedZone(DefaultZone, 7*60*60)
}

// ZoneName is zone when this server can load it, else DefaultZone: the zone
// a new series is stored with.
func ZoneName(zone string) string {
	if zone != "" && zone != "Local" {
		if _, err := time.LoadLocation(zone); err == nil {
			return zone
		}
	}
	return DefaultZone
}

// Weekday is an ISO 8601 weekday, Monday 1 to Sunday 7.
type Weekday int

// WeekdayOf is the ISO weekday of day.
func WeekdayOf(day time.Time) Weekday {
	if wd := day.Weekday(); wd != time.Sunday {
		return Weekday(wd)
	}
	return 7
}

// Valid reports whether w is 1 to 7.
func (w Weekday) Valid() bool { return w >= 1 && w <= 7 }

// Clock is a wall-clock time, read in the zone of its series or session.
type Clock struct {
	Hour, Minute int
}

// ParseClock reads "HH:MM" on a 24-hour clock.
func ParseClock(s string) (Clock, error) {
	t, err := time.Parse("15:04", s)
	if err != nil || len(s) != 5 {
		return Clock{}, fmt.Errorf("timetable: %q is not a time of day", s)
	}
	return Clock{Hour: t.Hour(), Minute: t.Minute()}, nil
}

// ClockOf is the wall-clock time of t in its own location.
func ClockOf(t time.Time) Clock { return Clock{Hour: t.Hour(), Minute: t.Minute()} }

// String is c as "HH:MM".
func (c Clock) String() string { return fmt.Sprintf("%02d:%02d", c.Hour, c.Minute) }

// On is the instant c falls at on day in loc, built with time.Date, so the
// zone's offset on that day is the zone database's, never worked out here.
func (c Clock) On(day time.Time, loc *time.Location) time.Time {
	y, m, d := day.Date()
	return time.Date(y, m, d, c.Hour, c.Minute, 0, 0, loc)
}

// Series is a class's weekly session: one weekday, start time, length and
// room, from StartsOn to EndsOn (nil until the teacher stops it), both
// inclusive and read in TimeZone. ClassName is the class's name and
// ClassArchivedAt when it was archived (nil while it is not), as read with it.
type Series struct {
	ID              string
	ClassID         string
	ClassName       string
	Weekday         Weekday
	Start           Clock
	DurationMinutes int
	Room            *string
	StartsOn        time.Time
	EndsOn          *time.Time
	TimeZone        string
	ClassArchivedAt *time.Time
}

// Location is the zone the series' dates and times are read in.
func (s Series) Location() *time.Location { return LocationOf(s.TimeZone) }

// StartsAt is the instant the series meets on day, a day it meets or not.
func (s Series) StartsAt(day time.Time) time.Time { return s.Start.On(day, s.Location()) }

// Meets reports whether day is one of the series' dates: its weekday, on or
// after StartsOn and on or before EndsOn.
func (s Series) Meets(day time.Time) bool {
	day = Day(day)
	if WeekdayOf(day) != s.Weekday || day.Before(s.StartsOn) {
		return false
	}
	return s.EndsOn == nil || !day.After(*s.EndsOn)
}

// ActiveOn reports whether the series still meets on or after the calendar
// day it is at now in its own zone: it has no last date, or its last date is
// that day or later. A series that has not begun yet is active. A series of a
// class archived by now is not: the class has no session from its archiving
// on (Archived), so it has no schedule going forward until it is restored.
func (s Series) ActiveOn(now time.Time) bool {
	if s.ClassArchivedAt != nil && !now.Before(*s.ClassArchivedAt) {
		return false
	}
	return s.EndsOn == nil || !s.EndsOn.Before(Today(now, s.Location()))
}

// TextPatch is an edit of an optional text: Set is false when the request left
// the field out, and a nil Value clears it.
type TextPatch struct {
	Set   bool
	Value *string
}

// Text is raw as stored: trimmed and composed to NFC, and none at all when
// nothing is left, so a column's length check never meets an empty string. A
// value over limit once composed is refused naming field.
func Text(c *content.Composer, field string, raw *string, limit int) *string {
	if raw == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*raw)
	if trimmed == "" {
		return nil
	}
	composed := c.Text(field, trimmed, limit)
	return &composed
}

// SameText reports whether a and b are the same optional text.
func SameText(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// Draft is a new session as the teacher writes it: a date and start time read
// in the caller's zone, a length, a room (Room.Set false takes the class's
// own), and with RepeatWeekly a weekly series from Date to EndsOn.
type Draft struct {
	Date            time.Time
	Start           Clock
	DurationMinutes int
	Room            TextPatch
	RepeatWeekly    bool
	EndsOn          *time.Time
}

// Validate refuses, naming the field, a Date more than two years after today,
// an EndsOn without RepeatWeekly, and an EndsOn before Date or more than
// MaxSeriesDays after it.
func (d Draft) Validate(today time.Time) error {
	var refused validation.Error
	if Day(d.Date).After(today.AddDate(2, 0, 0)) {
		refused.AddIn("date", "Không thể xếp buổi học quá hai năm tới.", "A session cannot be scheduled more than two years ahead.")
	}
	if d.EndsOn != nil {
		end := Day(*d.EndsOn)
		switch {
		case !d.RepeatWeekly:
			refused.AddIn("endsOn", "Chỉ buổi học lặp lại hằng tuần mới có ngày kết thúc.", "Only a weekly session has an end date.")
		case end.Before(Day(d.Date)):
			refused.AddIn("endsOn", "Ngày kết thúc không được trước ngày bắt đầu.", "The end date cannot be before the first date.")
		case end.After(Day(d.Date).AddDate(0, 0, MaxSeriesDays)):
			refused.AddIn("endsOn", "Buổi học lặp lại không kéo dài quá hai năm.", "A weekly session cannot run for more than two years.")
		}
	}
	if invalid := refused.OrNil(); invalid != nil {
		return invalid
	}
	return nil
}

// Series is the weekly series the draft starts for classID in zone: on the
// weekday of Date, from Date to EndsOn, else to termEnd (the last day of the
// term that holds Date, bounded to MaxSeriesDays), else open-ended, meeting in
// room.
func (d Draft) Series(classID, zone string, termEnd *time.Time, room *string) Series {
	first := Day(d.Date)
	s := Series{
		ClassID: classID, Weekday: WeekdayOf(first), Start: d.Start, DurationMinutes: d.DurationMinutes,
		Room: room, StartsOn: first, TimeZone: zone,
	}
	switch {
	case d.EndsOn != nil:
		end := Day(*d.EndsOn)
		s.EndsOn = &end
	case termEnd != nil:
		end := Day(*termEnd)
		if limit := first.AddDate(0, 0, MaxSeriesDays); end.After(limit) {
			end = limit
		}
		s.EndsOn = &end
	}
	return s
}

// SeriesChange is a change to a weekly series from a date on: a nil field and
// a Room that is not Set keep the series' value.
type SeriesChange struct {
	Weekday         *Weekday
	Start           *Clock
	DurationMinutes *int
	Room            TextPatch
}

// Over is the series c makes of s, and the fields, in the contract's words,
// that differ from it.
func (c SeriesChange) Over(s Series) (Series, []string) {
	next := s
	var fields []string
	if c.Weekday != nil && *c.Weekday != s.Weekday {
		next.Weekday = *c.Weekday
		fields = append(fields, "weekday")
	}
	if c.Start != nil && *c.Start != s.Start {
		next.Start = *c.Start
		fields = append(fields, "startTime")
	}
	if c.DurationMinutes != nil && *c.DurationMinutes != s.DurationMinutes {
		next.DurationMinutes = *c.DurationMinutes
		fields = append(fields, "durationMinutes")
	}
	if c.Room.Set && !SameText(c.Room.Value, s.Room) {
		next.Room = c.Room.Value
		fields = append(fields, "room")
	}
	return next, fields
}

// Split is how a change to series Old from a date on is stored. From is the
// first date the change applies to. With InPlace, nothing of Old has come yet
// (From is on or before its first date), so Next is Old itself, changed; else
// Old ends the day before From and Next is a new series from From to Old's last
// date. Either way, Old's per-date rows that Old does not keep (Keeps) are
// dropped.
type Split struct {
	Old     Series
	Next    Series
	From    time.Time
	InPlace bool
	Fields  []string
}

// DayStart is the instant day begins in the series' zone.
func (s Series) DayStart(day time.Time) time.Time { return Clock{}.On(day, s.Location()) }

// Keeps reports whether s keeps its per-date row r when it stops meeting
// before from: r has attendance, or it starts before from begins (it was moved
// earlier, and may have happened). The past is never rewritten.
func (s Series) Keeps(r Row, from time.Time) bool {
	return r.HasAttendance || r.StartsAt.Before(s.DayStart(from))
}

// SplitAt plans change to s from the date from, judged on today in the
// series' zone, over rows, the series' per-date rows: from before today is
// refused naming fromDate. The change starts after the scheduled date of
// every row from then on that s keeps, so the new series never computes a
// date the old one still holds: tomorrow when today's occurrence has
// attendance. A start after the series' last date is refused naming
// fromDate. A change that alters nothing has no Fields, and nothing is to be
// written.
func (s Series) SplitAt(change SeriesChange, from, today time.Time, rows []Row) (Split, error) {
	from, today = Day(from), Day(today)
	if from.Before(today) {
		return Split{}, validation.Refuse("fromDate", "Chỉ đổi được buổi học từ hôm nay trở đi.", "A weekly session can be changed from today on only.")
	}
	from = s.after(from, rows)
	if s.EndsOn != nil && from.After(*s.EndsOn) {
		return Split{}, validation.Refuse("fromDate", "Ngày này sau buổi cuối của lịch học.", "This date is after the weekly session's last date.")
	}
	next, fields := change.Over(s)
	out := Split{Old: s, From: from, Fields: fields, InPlace: !from.After(s.StartsOn)}
	if !out.InPlace {
		next.ID = ""
		next.StartsOn = from
		before := from.AddDate(0, 0, -1)
		out.Old.EndsOn = &before
	}
	out.Next = next
	return out, nil
}

func (s Series) after(from time.Time, rows []Row) time.Time {
	for moved := true; moved; {
		moved = false
		for _, r := range rows {
			if r.SeriesDate != nil && !r.SeriesDate.Before(from) && s.Keeps(r, from) {
				from, moved = r.SeriesDate.AddDate(0, 0, 1), true
			}
		}
	}
	return from
}

// Ending is how a series stops at a last date: with Remove, the series has not
// begun and the last date is before its first, so none of it has happened and
// the series goes.
type Ending struct {
	LastDate time.Time
	Remove   bool
}

// EndOn plans ending s on last, judged on today in the series' zone. Refused
// naming lastDate: a date before yesterday, since the past is never rewritten;
// one after the series' current last date, since an end never lengthens a
// series (the old half of a split would meet again beside the new one); and
// one more than MaxSeriesDays after its first date.
func (s Series) EndOn(last, today time.Time) (Ending, error) {
	last, today = Day(last), Day(today)
	switch {
	case last.Before(today.AddDate(0, 0, -1)):
		return Ending{}, validation.Refuse("lastDate", "Không thể dừng lịch học trước hôm qua.", "A weekly session cannot stop before yesterday.")
	case s.EndsOn != nil && last.After(*s.EndsOn):
		return Ending{}, validation.Refuse("lastDate", "Ngày này sau buổi cuối của lịch học.", "This date is after the weekly session's last date.")
	case last.After(s.StartsOn.AddDate(0, 0, MaxSeriesDays)):
		return Ending{}, validation.Refuse("lastDate", "Buổi học lặp lại không kéo dài quá hai năm.", "A weekly session cannot run for more than two years.")
	}
	return Ending{LastDate: last, Remove: last.Before(s.StartsOn) && !s.StartsOn.Before(today)}, nil
}
