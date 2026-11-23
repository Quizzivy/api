package domain

import (
	"context"
	"errors"
	"strings"
	"time"

	"quizzivy/internal/shared/validation"
)

// Term is a named run of calendar days of the centre's year (T-R8.3, DG-93):
// organisation-wide, both ends included, and never sharing a day with another
// term, which the database holds (terms_no_overlap). A day is held as its
// midnight in UTC, as a DATE column reads, so the zone a day is counted in is
// chosen when today is worked out (Today), never stored with it.
type Term struct {
	ID       string
	Name     string
	StartsOn time.Time
	EndsOn   time.Time
}

// TermState is where a term stands against a day.
type TermState string

// The states, as the contract names them.
const (
	TermCurrent  TermState = "current"
	TermUpcoming TermState = "upcoming"
	TermPast     TermState = "past"
)

// StateOn is the term's state on day: upcoming before its first day, past
// after its last, and current on every day from the first to the last,
// both included.
func (t Term) StateOn(day time.Time) TermState {
	d := Day(day)
	switch {
	case d.Before(t.StartsOn):
		return TermUpcoming
	case d.After(t.EndsOn):
		return TermPast
	default:
		return TermCurrent
	}
}

// StatedTerm is a term and its state on the viewer's today.
type StatedTerm struct {
	Term
	State TermState
}

// Holds reports whether day is one of the term's days.
func (t Term) Holds(day time.Time) bool { return t.StateOn(day) == TermCurrent }

// DefaultZone is the calendar zone today is counted in for an account that has
// stored none: the centre's.
const DefaultZone = "Asia/Ho_Chi_Minh"

// LocationOf loads zone, or DefaultZone when zone is empty or names no zone
// this server has, or the fixed offset of DefaultZone on a host without zone
// data.
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

// Day is the calendar day t falls on in its own location, as midnight UTC: the
// form a term's days are held in.
func Day(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// Today is the calendar day it is at now in loc.
func Today(now time.Time, loc *time.Location) time.Time { return Day(now.In(loc)) }

// TermDraft is a term as an admin writes it: a name and the first and last day.
type TermDraft struct {
	Name     string
	StartsOn time.Time
	EndsOn   time.Time
}

// Normalised returns the draft with its name trimmed and its ends as days, or
// a *validation.Error naming name when it is blank once trimmed and endsOn
// when the term would end before it starts. Whether it overlaps another term
// is the database's to say, inside the write.
func (d TermDraft) Normalised() (TermDraft, error) {
	out := TermDraft{Name: strings.TrimSpace(d.Name), StartsOn: Day(d.StartsOn), EndsOn: Day(d.EndsOn)}
	var refused validation.Error
	if out.Name == "" {
		refused.AddIn("name", "Hãy đặt tên cho học kỳ.", "The term needs a name.")
	}
	if out.EndsOn.Before(out.StartsOn) {
		refused.AddIn("endsOn", "Ngày kết thúc không được trước ngày bắt đầu.", "The term cannot end before it starts.")
	}
	if err := refused.OrNil(); err != nil {
		return TermDraft{}, err
	}
	return out, nil
}

// TermPatch is a change to a term: a nil field keeps the stored value.
type TermPatch struct {
	Name     *string
	StartsOn *time.Time
	EndsOn   *time.Time
}

// Over is the draft the patch makes of stored: the range is judged as it
// stands after the change.
func (p TermPatch) Over(stored Term) TermDraft {
	out := TermDraft{Name: stored.Name, StartsOn: stored.StartsOn, EndsOn: stored.EndsOn}
	if p.Name != nil {
		out.Name = *p.Name
	}
	if p.StartsOn != nil {
		out.StartsOn = *p.StartsOn
	}
	if p.EndsOn != nil {
		out.EndsOn = *p.EndsOn
	}
	return out
}

// ChangedFields names, in the contract's words, what next changes of t.
func (t Term) ChangedFields(next Term) []string {
	var out []string
	if t.Name != next.Name {
		out = append(out, "name")
	}
	if !t.StartsOn.Equal(next.StartsOn) {
		out = append(out, "startsOn")
	}
	if !t.EndsOn.Equal(next.EndsOn) {
		out = append(out, "endsOn")
	}
	return out
}

// ErrTermNotFound is a term id no row has.
var ErrTermNotFound = errors.New("organization: no such term")

// TermOverlap is a write refused because the range shares a day with Other,
// as the database's exclusion constraint judged it. Nothing was stored.
type TermOverlap struct {
	Other Term
}

func (e *TermOverlap) Error() string {
	return "organization: the term overlaps " + e.Other.ID + " (" + e.Other.Name + ")"
}

// TermWriter is who writes a term, from where and when, as its audit row
// records it.
type TermWriter struct {
	ActorID   string
	IP        string
	UserAgent string
	Now       time.Time
}

// Terms stores the centre's terms. Each write runs in one transaction with its
// audit row: term.created, term.updated or term.deleted, entity term. A write
// whose range shares a day with another term fails with *TermOverlap naming
// it, decided by the database (23P01), never checked beforehand.
type Terms interface {
	// List returns every term, newest first: by first day, latest first.
	List(ctx context.Context) ([]Term, error)
	// On returns the term that holds day, or nil when none does.
	On(ctx context.Context, day time.Time) (*Term, error)
	// EndingFrom returns the terms whose last day is day or later, earliest first.
	EndingFrom(ctx context.Context, day time.Time) ([]Term, error)
	// Create stores draft, already normalised.
	Create(ctx context.Context, draft TermDraft, by TermWriter) (Term, error)
	// Update locks term id and stores what change makes of it, or fails with
	// ErrTermNotFound, or with change's error. A draft equal to the stored term
	// writes nothing and returns the stored term.
	Update(ctx context.Context, id string, change func(Term) (TermDraft, error), by TermWriter) (Term, error)
	// Delete removes term id, or fails with ErrTermNotFound.
	Delete(ctx context.Context, id string, by TermWriter) error
}
