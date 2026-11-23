package domain

import (
	"time"

	"quizzivy/internal/shared/validation"
)

// Row is one stored row of app.class_sessions: a one-off (SeriesID nil), or
// one occurrence of a series named by SeriesID and SeriesDate, the date it was
// scheduled for, however StartsAt has moved it. An occurrence's Room is the
// date's own, nil when the series' room applies. ClassName is the class's name
// and ClassArchivedAt when it was archived, as read with it.
type Row struct {
	ID              string
	ClassID         string
	ClassName       string
	SeriesID        *string
	SeriesDate      *time.Time
	StartsAt        time.Time
	DurationMinutes int
	Room            *string
	Note            *string
	CancelledAt     *time.Time
	HasAttendance   bool
	ClassArchivedAt *time.Time
}

// Archived reports whether a session of a class archived at archivedAt (nil:
// not archived) that starts at startsAt is gone: an archived class has no
// session from the moment it was archived on. The ones before stay, with
// their attendance, and restoring the class brings the later ones back.
func Archived(startsAt time.Time, archivedAt *time.Time) bool {
	return archivedAt != nil && !startsAt.Before(*archivedAt)
}

// Exists reports whether the session f names exists: it does not start at or
// after its class was archived (Archived).
func (f Found) Exists() bool {
	archived := (*time.Time)(nil)
	switch {
	case f.Series != nil:
		archived = f.Series.ClassArchivedAt
	case f.Row != nil:
		archived = f.Row.ClassArchivedAt
	}
	return !Archived(f.Fields().StartsAt, archived)
}

// Session is one session as the contract reads it (ClassSession): a one-off,
// or an occurrence of Series with any change made to its date. Date is the day
// it happens on now, in its series' zone or, for a one-off, the viewer's.
// RowID is the row behind it, empty for an occurrence that has none.
type Session struct {
	ID              SessionID
	ClassID         string
	ClassName       string
	Series          *Series
	Date            time.Time
	StartsAt        time.Time
	DurationMinutes int
	Room            *string
	Note            *string
	Cancelled       bool
	Changed         bool
	HasAttendance   bool
	RowID           string
}

// Found is a session id resolved under the caller's scope: its series for an
// occurrence, and its row when it has one. An occurrence of Series without a
// Row is computed for the date.
type Found struct {
	ID     SessionID
	Series *Series
	Row    *Row
}

// Patch is a change to one session (SessionPatch): a nil field, or a Room or
// Note that is not Set, keeps its value.
type Patch struct {
	Date            *time.Time
	Start           *Clock
	DurationMinutes *int
	Room            TextPatch
	Note            TextPatch
	Cancelled       *bool
}

// Fields is what a session's row holds that a patch can change.
type Fields struct {
	StartsAt        time.Time
	DurationMinutes int
	Room            *string
	Note            *string
	CancelledAt     *time.Time
}

// Edit is what a patch makes of a session's row. Store false means the row is
// not needed (an occurrence back to its series, uncancelled, with no
// attendance): an existing one is deleted and none is made. Changed false
// means nothing differs from the stored session, so nothing is written.
type Edit struct {
	Fields    Fields
	Store     bool
	Changed   bool
	Cancelled bool
	Names     []string
}

// FieldsOf is what row holds of the fields a patch changes.
func FieldsOf(r Row) Fields {
	return Fields{StartsAt: r.StartsAt, DurationMinutes: r.DurationMinutes, Room: r.Room, Note: r.Note, CancelledAt: r.CancelledAt}
}

// Apply is the edit p makes of session f, read in viewer for a one-off and in
// its series' zone for an occurrence, at now. A Date more than two years after
// that zone's today is refused naming date.
func (p Patch) Apply(f Found, viewer *time.Location, now time.Time) (Edit, error) {
	loc := viewer
	if f.Series != nil {
		loc = f.Series.Location()
	}
	if p.Date != nil && Day(*p.Date).After(Today(now, loc).AddDate(2, 0, 0)) {
		return Edit{}, validation.Refuse("date", "Không thể dời buổi học quá hai năm tới.", "A session cannot be moved more than two years ahead.")
	}
	current := f.Fields()
	next := p.over(current, loc, now)
	names := changedNames(current, next)
	out := Edit{
		Fields: next, Changed: len(names) > 0, Names: names,
		Cancelled: current.CancelledAt == nil && next.CancelledAt != nil,
		Store:     true,
	}
	attended := f.Row != nil && f.Row.HasAttendance
	if f.Series != nil && !attended && next.CancelledAt == nil && !differs(*f.Series, f.ID.Date, next) {
		out.Store = false
	}
	return out, nil
}

// Fields is what the session holds now: its row's, or, for an occurrence
// without one, its series' on the date.
func (f Found) Fields() Fields {
	if f.Row != nil {
		return FieldsOf(*f.Row)
	}
	if f.Series != nil {
		return Fields{StartsAt: f.Series.StartsAt(f.ID.Date), DurationMinutes: f.Series.DurationMinutes}
	}
	return Fields{}
}

func (p Patch) over(current Fields, loc *time.Location, now time.Time) Fields {
	next := current
	local := current.StartsAt.In(loc)
	day, clock := Day(local), ClockOf(local)
	if p.Date != nil {
		day = Day(*p.Date)
	}
	if p.Start != nil {
		clock = *p.Start
	}
	next.StartsAt = clock.On(day, loc)
	if p.DurationMinutes != nil {
		next.DurationMinutes = *p.DurationMinutes
	}
	if p.Room.Set {
		next.Room = p.Room.Value
	}
	if p.Note.Set {
		next.Note = p.Note.Value
	}
	switch {
	case p.Cancelled == nil:
	case !*p.Cancelled:
		next.CancelledAt = nil
	case next.CancelledAt == nil:
		at := now
		next.CancelledAt = &at
	}
	return next
}

func changedNames(a, b Fields) []string {
	var out []string
	if !a.StartsAt.Equal(b.StartsAt) {
		out = append(out, "startsAt")
	}
	if a.DurationMinutes != b.DurationMinutes {
		out = append(out, "durationMinutes")
	}
	if !SameText(a.Room, b.Room) {
		out = append(out, "room")
	}
	if !SameText(a.Note, b.Note) {
		out = append(out, "note")
	}
	if (a.CancelledAt == nil) != (b.CancelledAt == nil) {
		out = append(out, "cancelled")
	}
	return out
}

func differs(s Series, scheduled time.Time, f Fields) bool {
	return !f.StartsAt.Equal(s.StartsAt(scheduled)) || f.DurationMinutes != s.DurationMinutes ||
		(f.Room != nil && !SameText(f.Room, s.Room)) || f.Note != nil
}
