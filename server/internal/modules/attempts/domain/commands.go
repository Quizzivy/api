package domain

import (
	"time"
)

type SaveInput struct {
	AttemptID string
	StudentID string
	SessionID string
	Answers   []Answer
	Events    []Event
	// ActiveMs is the paper's visible time since this session's previous
	// report, as the engine counts it; nil credits nothing.
	ActiveMs *int
}

// SaveResult reports what actually landed. Saved can be lower than the number
// of answers submitted -- see Postgres.Save for why that is not an error.
type SaveResult struct {
	SavedAt time.Time
	// DeadlineAt is the attempt's deadline as the save found it.
	DeadlineAt time.Time
	Saved      int
	// Dropped names the answers that did not land.
	Dropped []string
}

// FlushInput carries the events and exactly one credential.
type FlushInput struct {
	AttemptID string
	SessionID string
	Events    []Event
	// ActiveMs is as SaveInput's; it is credited only from the attempt's
	// current session.
	ActiveMs *int

	// StudentID is set when the request arrived with a verified access token.
	StudentID   string
	BeaconToken string
}

// CreateInput starts an attempt. ExpectedDeadlineAt is where the span ends
// that the caller worked out from its own earlier read of the rules
// (Rules.GuardedUntil), which the maintenance check looks at first; the
// deadline stored is the one the rules give when Create reads them again
// under a lock, at StartedAt.
type CreateInput struct {
	AssignmentID       string
	StudentID          string
	AttemptNo          int
	SessionID          string
	Seed               int64
	BeaconHash         []byte
	StartedAt          time.Time
	ExpectedDeadlineAt time.Time
}

type ResumeInput struct {
	AttemptID  string
	SessionID  string
	BeaconHash []byte
	Now        time.Time
}
