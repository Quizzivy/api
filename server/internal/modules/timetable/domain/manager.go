package domain

import (
	"slices"
	"strings"
	"time"
)

// OccurrenceManager computes sessions from series and rows. Viewer is the zone
// a one-off's date is read in, the caller's; an occurrence's date is read in
// its series' zone. It reads nothing and writes nothing.
type OccurrenceManager struct {
	Viewer *time.Location
}

func (m OccurrenceManager) viewer() *time.Location {
	if m.Viewer == nil {
		return LocationOf("")
	}
	return m.Viewer
}

// Expand is every session that starts in [from, to): each series' dates
// walked in the series' zone from the later of from's day and its first date
// to the earlier of to's day and its last date, each start built from the
// local date, start time and zone; a stored row in place of the occurrence it
// names by (series_id, series_date), on the date it now falls on, so a moved
// occurrence appears on its new date only; and the one-offs. The result is
// sorted by start, then id.
//
// rows must hold every row of the series' classes that starts in the range or
// names an occurrence whose date the range covers in its zone (a day either
// side of from and to is enough), and series every series those rows name: a
// row whose series is not among series is left out.
func (m OccurrenceManager) Expand(series []Series, rows []Row, from, to time.Time) []Session {
	bySeries := make(map[string]*Series, len(series))
	for i := range series {
		bySeries[series[i].ID] = &series[i]
	}
	out, stored := m.stored(bySeries, rows, from, to)
	for i := range series {
		out = m.walk(out, series[i], stored, from, to)
	}
	slices.SortFunc(out, func(a, b Session) int {
		if c := a.StartsAt.Compare(b.StartsAt); c != 0 {
			return c
		}
		return strings.Compare(a.ID.String(), b.ID.String())
	})
	return out
}

func (m OccurrenceManager) stored(bySeries map[string]*Series, rows []Row, from, to time.Time) ([]Session, map[string]bool) {
	stored := make(map[string]bool, len(rows))
	var out []Session
	for _, r := range rows {
		var s *Series
		if r.SeriesID != nil {
			if s = bySeries[*r.SeriesID]; s == nil {
				continue
			}
			stored[OccurrenceID(s.ID, *r.SeriesDate).String()] = true
		}
		if in(r.StartsAt, from, to) {
			out = append(out, m.fromRow(s, r))
		}
	}
	return out, stored
}

func (m OccurrenceManager) walk(out []Session, s Series, stored map[string]bool, from, to time.Time) []Session {
	loc := s.Location()
	first, last := Day(from.In(loc)), Day(to.In(loc))
	if s.StartsOn.After(first) {
		first = s.StartsOn
	}
	if s.EndsOn != nil && s.EndsOn.Before(last) {
		last = *s.EndsOn
	}
	for day := first.AddDate(0, 0, (int(s.Weekday)-int(WeekdayOf(first))+7)%7); !day.After(last); day = day.AddDate(0, 0, 7) {
		if stored[OccurrenceID(s.ID, day).String()] {
			continue
		}
		if session := m.Occurrence(s, nil, day); in(session.StartsAt, from, to) {
			out = append(out, session)
		}
	}
	return out
}

func in(t, from, to time.Time) bool { return !t.Before(from) && t.Before(to) }

// Occurrence is the occurrence of s scheduled for day: as row stores it when
// row is not nil, else as the series computes it.
func (m OccurrenceManager) Occurrence(s Series, row *Row, day time.Time) Session {
	if row != nil {
		return m.fromRow(&s, *row)
	}
	day = Day(day)
	series := s
	return Session{
		ID: OccurrenceID(s.ID, day), ClassID: s.ClassID, ClassName: s.ClassName, Series: &series,
		Date: day, StartsAt: s.StartsAt(day), DurationMinutes: s.DurationMinutes, Room: s.Room,
	}
}

// Session is the session f names: its row, or the occurrence its series
// computes for the date.
func (m OccurrenceManager) Session(f Found) Session {
	if f.Series != nil {
		return m.Occurrence(*f.Series, f.Row, f.ID.Date)
	}
	return m.fromRow(nil, *f.Row)
}

func (m OccurrenceManager) fromRow(s *Series, r Row) Session {
	out := Session{
		ClassID: r.ClassID, ClassName: r.ClassName, StartsAt: r.StartsAt, DurationMinutes: r.DurationMinutes,
		Room: r.Room, Note: r.Note, Cancelled: r.CancelledAt != nil, HasAttendance: r.HasAttendance, RowID: r.ID,
	}
	if s == nil {
		out.ID = OneOffID(r.ID)
		out.Date = Day(r.StartsAt.In(m.viewer()))
		return out
	}
	series := *s
	out.ID = OccurrenceID(s.ID, *r.SeriesDate)
	out.Series = &series
	out.Date = Day(r.StartsAt.In(s.Location()))
	if r.Room == nil {
		out.Room = s.Room
	}
	out.Changed = differs(*s, *r.SeriesDate, FieldsOf(r))
	return out
}
