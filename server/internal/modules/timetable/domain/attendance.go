package domain

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"quizzivy/internal/shared/access"
	"quizzivy/internal/shared/content"
)

// Status is a student's mark on a session (DG-92): present, late or absent.
// Unmarked is no status at all, the absence of a mark, and stays out of the
// rate; excused is not built.
type Status string

const (
	Present Status = "present"
	Late    Status = "late"
	Absent  Status = "absent"
)

// Valid reports whether s is one of the three statuses.
func (s Status) Valid() bool { return s == Present || s == Late || s == Absent }

// Attended reports whether s counts as attended in the rate: present or late.
func (s Status) Attended() bool { return s == Present || s == Late }

// Grace is how long before its start a session takes attendance, MaxMarkNote
// the most characters a mark's note holds (attendance_marks_note_check),
// MaxMarks the most marks one save names, and PageSize the sessions a page of
// the register list holds by default.
const (
	Grace       = 15 * time.Minute
	MaxMarkNote = 500
	MaxMarks    = 200
	PageSize    = 20
)

// The refusals of a save that are about the session, not the marks.
var (
	ErrNotStarted = errors.New("timetable: the session starts more than 15 minutes from now")
	ErrCancelled  = errors.New("timetable: the session is cancelled")
)

// Refusal is why a save refuses the marks a *RefusedMarks names.
type Refusal int

const (
	// RefusedTwice: a student is named more than once.
	RefusedTwice Refusal = iota + 1
	// RefusedNoteWithoutStatus: a note rides beside a null status.
	RefusedNoteWithoutStatus
	// RefusedNoteTooLong: a note is over MaxMarkNote characters once composed.
	RefusedNoteTooLong
	// RefusedOffRoster: a student is neither an active member of the class
	// nor already marked on the session, which is also how a student who
	// does not exist, or whom the caller may not know, answers.
	RefusedOffRoster
)

// RefusedMarks refuses a whole save for the students it names, each once, in
// the order the request named them; nothing is written.
type RefusedMarks struct {
	Reason     Refusal
	StudentIDs []string
}

func (e *RefusedMarks) Error() string {
	return "timetable: marks refused for " + strings.Join(e.StudentIDs, ", ")
}

// MarkInput is one student's mark as a save names it: a nil Status unmarks
// them, and a Note that is not Set keeps the stored one.
type MarkInput struct {
	StudentID string
	Status    *Status
	Note      TextPatch
}

// CleanMarks is marks with every note trimmed and composed to NFC, a blank one
// clearing the note as null does. It refuses, in this order and each naming
// the students concerned: a student named twice, a note beside a null status,
// and a note over MaxMarkNote characters.
func CleanMarks(marks []MarkInput) ([]MarkInput, error) {
	seen := make(map[string]int, len(marks))
	var twice, noted, long []string
	out := make([]MarkInput, len(marks))
	for i, m := range marks {
		if seen[m.StudentID]++; seen[m.StudentID] == 2 {
			twice = append(twice, m.StudentID)
		}
		if m.Status == nil && m.Note.Set && m.Note.Value != nil {
			noted = append(noted, m.StudentID)
		}
		if m.Note.Set && m.Note.Value != nil {
			m.Note.Value = markNote(*m.Note.Value)
			if m.Note.Value != nil && utf8.RuneCountInString(*m.Note.Value) > MaxMarkNote {
				long = append(long, m.StudentID)
			}
		}
		out[i] = m
	}
	switch {
	case twice != nil:
		return nil, &RefusedMarks{Reason: RefusedTwice, StudentIDs: twice}
	case noted != nil:
		return nil, &RefusedMarks{Reason: RefusedNoteWithoutStatus, StudentIDs: noted}
	case long != nil:
		return nil, &RefusedMarks{Reason: RefusedNoteTooLong, StudentIDs: long}
	}
	return out, nil
}

func markNote(raw string) *string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}
	composed := content.NFC(trimmed)
	return &composed
}

// Changes reports whether marks would set, change or remove anything on a
// session that has no mark yet: any mark that names a status does.
func Changes(marks []MarkInput) bool {
	return slices.ContainsFunc(marks, func(m MarkInput) bool { return m.Status != nil })
}

// TakesAttendance refuses a session that is cancelled (ErrCancelled), or that
// starts more than Grace after now (ErrNotStarted).
func (f Found) TakesAttendance(now time.Time) error {
	fields := f.Fields()
	switch {
	case fields.CancelledAt != nil:
		return ErrCancelled
	case fields.StartsAt.After(now.Add(Grace)):
		return ErrNotStarted
	}
	return nil
}

// Tally is one student's marks over the sessions a rate covers.
type Tally struct {
	Attended int
	Counted  int
}

// Rate is the tally's (present + late) ÷ (present + late + absent) as a whole
// percent rounded half up, or nil when no mark was counted.
func (t Tally) Rate() *int {
	if t.Counted <= 0 {
		return nil
	}
	rate := (200*t.Attended + t.Counted) / (2 * t.Counted)
	return &rate
}

// RateWindow is the instants a rate counts the sessions of: those that start
// in [From, To), the days of the current term in the caller's zone, or every
// session when Overall.
type RateWindow struct {
	Overall  bool
	From, To time.Time
}

// TermRef is the term a register's rates cover.
type TermRef struct {
	ID   string
	Name string
}

// TermWindow is the window of the term whose first and last days are startsOn
// and endsOn (inclusive), read in loc.
func TermWindow(startsOn, endsOn time.Time, loc *time.Location) RateWindow {
	return RateWindow{From: Clock{}.On(Day(startsOn), loc), To: Clock{}.On(Day(endsOn).AddDate(0, 0, 1), loc)}
}

// RosterStudent is one student of a register: an active member of the class,
// or a student marked on the session. Status and Note are their mark, nil
// while unmarked, and Tally their marks over the register's rate window.
type RosterStudent struct {
	StudentID string
	FullName  string
	AvatarKey *string
	AvatarURL *string
	Status    *Status
	Note      *string
	Tally     Tally
}

// Register is one session's roster, sorted as the class members list is
// (latest joined first, then a marked student who is no member), each
// student's mark and their rate over Term, or over every session of the
// class when Term is nil.
type Register struct {
	Session  Session
	Term     *TermRef
	Students []RosterStudent
}

// RosterCounts is a register's roster by mark.
type RosterCounts struct {
	Present, Late, Absent, Unmarked int
}

// Counts is the roster by mark.
func (r Register) Counts() RosterCounts {
	var out RosterCounts
	for _, s := range r.Students {
		switch {
		case s.Status == nil:
			out.Unmarked++
		case *s.Status == Present:
			out.Present++
		case *s.Status == Late:
			out.Late++
		default:
			out.Absent++
		}
	}
	return out
}

// Counts is a session's marks by status.
type Counts struct {
	Present, Late, Absent int
}

// SessionFacts is what the register list shows of a session beyond the
// session itself: the students its register lists, and its marks by status
// (nil when it has none).
type SessionFacts struct {
	StudentCount int
	Counts       *Counts
}

// ListedSession is one row of the register list.
type ListedSession struct {
	Session
	SessionFacts
}

// SessionFilter is what the register list keeps: the sessions that start at
// or after Since and before Before, with attendance or without when Marked is
// set, Limit to a page (PageSize when it is not positive).
type SessionFilter struct {
	Since, Before *time.Time
	Marked        *bool
	Limit         int
}

// SessionPage is one page of the register list: Items newest first, NextBefore
// the last item's start when older sessions match, and NotMarked the started
// sessions without attendance in the whole range read, whatever the filter.
type SessionPage struct {
	Items      []ListedSession
	NextBefore *time.Time
	NotMarked  int
}

// Started is the sessions of all that count for the register at now: begun
// (StartsAt at or before now) and not cancelled, newest first, then by id.
func Started(all []Session, now time.Time) []Session {
	out := make([]Session, 0, len(all))
	for _, s := range all {
		if !s.Cancelled && !s.StartsAt.After(now) {
			out = append(out, s)
		}
	}
	slices.SortStableFunc(out, func(a, b Session) int {
		if c := b.StartsAt.Compare(a.StartsAt); c != 0 {
			return c
		}
		return strings.Compare(b.ID.String(), a.ID.String())
	})
	return out
}

// PageOf is the page f keeps of started, the register's sessions newest first
// (Started), with NotMarked counted over all of them. A page never splits the
// sessions that start at one instant, so it may hold more than the limit.
func PageOf(started []Session, f SessionFilter) SessionPage {
	limit := f.Limit
	if limit <= 0 {
		limit = PageSize
	}
	out := SessionPage{Items: []ListedSession{}}
	var kept []Session
	for _, s := range started {
		if !s.HasAttendance {
			out.NotMarked++
		}
		if f.Since != nil && s.StartsAt.Before(*f.Since) || f.Before != nil && !s.StartsAt.Before(*f.Before) ||
			f.Marked != nil && *f.Marked != s.HasAttendance {
			continue
		}
		kept = append(kept, s)
	}
	n := min(limit, len(kept))
	for n < len(kept) && kept[n].StartsAt.Equal(kept[n-1].StartsAt) {
		n++
	}
	for _, s := range kept[:n] {
		out.Items = append(out.Items, ListedSession{Session: s})
	}
	if n < len(kept) {
		next := kept[n-1].StartsAt
		out.NextBefore = &next
	}
	return out
}

// Attendance keeps the marks in app.attendance_marks. Every save runs in one
// transaction under the class row's lock, then the series row's, the same
// locks every timetable write takes, so a save never races a split or an end
// that drops per-date rows; an occurrence gets its row under them, once
// however many saves race for it.
type Attendance interface {
	// Roster returns the register of the session f names (its class's
	// active student members and anyone marked on its row), sorted as the
	// class members list is, each student's tally over w, in one query.
	Roster(ctx context.Context, f Found, w RateWindow) ([]RosterStudent, error)
	// SaveMarks locks the session's class and series, refuses it through
	// check, refuses with *RefusedMarks the students neither an active
	// member nor marked on it, gives an occurrence its row when a mark needs
	// one, and writes marks with one attendance.saved row in the same
	// statement, or nothing when nothing changes.
	SaveMarks(ctx context.Context, scope access.Scope, id SessionID, check func(Found) error, marks []MarkInput, by Writer) error
	// ClassReached reports whether scope reaches class id.
	ClassReached(ctx context.Context, scope access.Scope, id string) (bool, error)
	// Earliest is the earliest of the first day of any series of w's classes
	// and the UTC day any of their stored rows starts on, or nil when the
	// classes have no session at all; w's From and To are not read. A day
	// either side of it covers every zone.
	Earliest(ctx context.Context, w Window) (*time.Time, error)
	// Facts returns, in one query, the student count and the marks of each
	// session, keyed by its id; sessions without a row are read as having no
	// mark.
	Facts(ctx context.Context, sessions []Session) (map[string]SessionFacts, error)
}
