package schedule

import "time"

// Mode is how an assignment times an attempt (F1,
// docs/plan/77f-f1-assignment-modes.md). Under Strict an attempt's deadline is
// the earlier of its start plus its student's time limit and its student's
// close, and the integrity policy decides what an absence does. Under
// Flexible the deadline is the student's close, and no absence submits or
// locks the paper. Every assignment made before F1 is Strict.
type Mode string

const (
	Strict   Mode = "strict"
	Flexible Mode = "flexible"
)

// Valid reports whether m is one of the two modes.
func (m Mode) Valid() bool { return m == Strict || m == Flexible }

// OrStrict is m, or Strict when m is empty: the mode a create without one
// stores.
func (m Mode) OrStrict() Mode {
	if m == "" {
		return Strict
	}
	return m
}

// Deadline is the deadline of an attempt started at start under mode m, for a
// student whose time limit is durationMin and whose close is closes (Close,
// their override included): the earlier of the two under Strict, the close
// under Flexible, where the time limit is stored and not applied.
func (m Mode) Deadline(start time.Time, durationMin int, closes time.Time) time.Time {
	if m == Flexible {
		return closes
	}
	full := start.Add(time.Duration(durationMin) * time.Minute)
	if closes.Before(full) {
		return closes
	}
	return full
}

// DeadlineOf is Deadline as SQL, over a row of app.attempts aliased at and
// its assignment aliased a, under the override aliased override: the time
// limit is the override's when it sets one.
func DeadlineOf(override string) string {
	return `CASE WHEN a.mode = 'flexible' THEN ` + CloseOf(override) + `
		       ELSE least(at.started_at + make_interval(mins => coalesce(` + override + `.duration_minutes, a.duration_minutes)),
		                  ` + CloseOf(override) + `) END`
}
