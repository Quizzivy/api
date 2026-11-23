package domain

import (
	"time"
)

// NewMember describes an account to create as part of enrolling. Nil when the
// student already has one.
type NewMember struct {
	Email          string
	FullName       string
	Provider       string
	ProviderUserID string
}

// EnrolInput names the typed code by every hash it may be stored under; the
// caller has already refused a code that normalises to nothing.
type EnrolInput struct {
	Code           JoinCodeLookup
	ExistingUserID string
	NewMember      *NewMember

	Now       time.Time
	IP        *string
	UserAgent *string
}

// EnrolResult reuses PreviewOutcome for its refusals, so /join/preview and the
// enrolment that follows it cannot drift into disagreeing about what a code's
// state means -- or into leaking different amounts about it.
type EnrolResult struct {
	Outcome       PreviewOutcome
	UserID        string
	AlreadyMember bool
	Class         EnrolledClass
	// StudentName is the joiner's full name and TeacherID the class's
	// teacher. Both are read in the enrolment's transaction, for the
	// notification that follows it.
	StudentName string
	TeacherID   string
}

// EnrolledClass is the §7 Class shape the join endpoints return. Timetable,
// read once the enrolment has committed, is nil until then; a pointer keeps
// an EnrolResult comparable, as a refusal is compared whole.
type EnrolledClass struct {
	ID              string
	Name            string
	Description     *string
	ScheduleLabel   *string
	Room            *string
	Timetable       *Timetable
	StudentCount    int
	SelfJoinEnabled bool
	CreatedAt       time.Time
}

// Slots are the class's weekly meeting times, none until Timetable is read.
func (c EnrolledClass) Slots() []ScheduleSlot {
	if c.Timetable == nil {
		return nil
	}
	return c.Timetable.Slots
}

// DerivedRoom is where the class meets, as Class.DerivedRoom.
func (c EnrolledClass) DerivedRoom() *string {
	if c.Timetable == nil {
		return c.Room
	}
	return RoomOf(c.Room, *c.Timetable)
}

// Meta is the request context §6.5 requires on every enrolment audit row.
type Meta struct {
	IP        string
	UserAgent string
}
