package domain

import (
	"time"
)

// Monitor is the §8 monitor screen's data: the roster, each with the attempt
// that stands for them.
type Monitor struct {
	ServerTime    time.Time
	QuestionCount int
	Rows          []MonitorRow
}

// MonitorRow is one targeted student on G-02, whether or not they have started.
type MonitorRow struct {
	StudentID string
	FullName  string
	// State is `not_started`, or the attempt's status.
	State          string
	AttemptID      *string
	AttemptNo      *int
	StartedAt      *time.Time
	DeadlineAt     *time.Time
	SubmittedAt    *time.Time
	AnsweredCount  *int
	Score          *Score
	FocusLossCount *int
	// ActiveSeconds is the attempt's time on the paper, rounded down; nil
	// when it was not measured (an attempt started before F1).
	ActiveSeconds  *int
	Flagged        bool
	AudioOverLimit bool
	// ExtendedTo is the student's own close when an override of theirs reaches
	// past the assignment's. While it is ahead the student may still start or
	// carry on, whatever the assignment has done.
	ExtendedTo *time.Time
}
