package domain

import "slices"

// Event is one switch of the notification settings, and one of the values
// notification_preferences_event_check allows.
type Event string

const (
	EventAttemptSubmitted  Event = "attempt.submitted"
	EventAttemptFlagged    Event = "attempt.flagged"
	EventAssignmentClosing Event = "assignment.closing"
	EventAssignmentDueSoon Event = "assignment.due_soon"
	EventResultReady       Event = "result.ready"
	EventMessageReceived   Event = "message.received"
	EventSummaryWeekly     Event = "summary.weekly"
)

var events = []Event{
	EventAttemptSubmitted, EventAttemptFlagged, EventAssignmentClosing, EventAssignmentDueSoon, EventResultReady,
	EventMessageReceived, EventSummaryWeekly,
}

// Events returns every switch, in the order a set of preferences is answered.
func Events() []Event {
	return slices.Clone(events)
}

// Preference is one switch for one user: whether the notices its event
// governs are written in the app, and whether the emails it governs are sent.
type Preference struct {
	Event Event
	InApp bool
	Email bool
}

// Recipient is whose switches a set of preferences is: a staff member's, whose
// two columns are separate, or a student's, whose one switch covers both
// channels (DG-82). A student is an account of R2's strict student predicate
// (access.IsStudentLike, app.student_like_roles).
type Recipient int

const (
	// Staff is anyone who is not a student.
	Staff Recipient = iota + 1
	// Student is an account of R2's strict student predicate.
	Student
)

// DefaultInApp is whether the notices event governs are written in the app
// for a user who never saved the switch: yes, but for the weekly summary,
// which the deck draws off in the app.
func DefaultInApp(event Event) bool {
	return event != EventSummaryWeekly
}

// staffEmail are the switches whose email a staff member gets until they save
// them, as the deck draws them: a flag, a message and the weekly summary, and
// the two student switches, which a staff member is not asked about.
var staffEmail = map[Event]bool{
	EventAttemptFlagged: true, EventMessageReceived: true, EventSummaryWeekly: true,
	EventAssignmentDueSoon: true, EventResultReady: true,
}

// DefaultPreference is the switch of a user who never saved it, as the deck
// draws it (NotificationEvent): in the app unless DefaultInApp says not, and
// by email for a student exactly when in the app, for a staff member for a
// flag, a message, the weekly summary and the two student switches, and not
// for a submission or a closing assignment.
func DefaultPreference(event Event, r Recipient) Preference {
	p := Preference{Event: event, InApp: DefaultInApp(event)}
	if r == Student {
		p.Email = p.InApp
	} else {
		p.Email = staffEmail[event]
	}
	return p
}

// Keep returns prefs as r's switches hold them: a student's with each email
// equal to its in-app switch, whatever was given or stored, and a staff
// member's unchanged.
func (r Recipient) Keep(prefs []Preference) []Preference {
	if r != Student {
		return prefs
	}
	out := make([]Preference, len(prefs))
	for i, p := range prefs {
		p.Email = p.InApp
		out[i] = p
	}
	return out
}

// WithDefaults returns every switch in Events' order, taking each from stored
// when it is there and r's default when it is not, as r keeps them (Keep). A
// stored row whose event is not a switch is left out.
func WithDefaults(stored []Preference, r Recipient) []Preference {
	saved := make(map[Event]Preference, len(stored))
	for _, p := range stored {
		saved[p.Event] = p
	}
	out := make([]Preference, 0, len(events))
	for _, event := range events {
		if p, ok := saved[event]; ok {
			out = append(out, p)
			continue
		}
		out = append(out, DefaultPreference(event, r))
	}
	return r.Keep(out)
}

// WholeSet returns given in Events' order when it holds every switch exactly
// once. It answers ErrUnknownEvent for an event that is not a switch, and
// ErrEventsNotOnceEach when one is repeated or missing.
func WholeSet(given []Preference) ([]Preference, error) {
	seen := make(map[Event]Preference, len(given))
	for _, p := range given {
		if !slices.Contains(events, p.Event) {
			return nil, ErrUnknownEvent
		}
		if _, twice := seen[p.Event]; twice {
			return nil, ErrEventsNotOnceEach
		}
		seen[p.Event] = p
	}
	if len(seen) != len(events) {
		return nil, ErrEventsNotOnceEach
	}
	out := make([]Preference, 0, len(events))
	for _, event := range events {
		out = append(out, seen[event])
	}
	return out, nil
}
