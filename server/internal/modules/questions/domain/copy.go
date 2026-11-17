package domain

import (
	"time"

	"quizzivy/internal/shared/access"
)

// Copy is a copy of live standalone bank questions ("Make a copy", D4,
// T-R7.7): each becomes a new bank question owned by OwnerID and created and
// last written by ActorID, with its options, blanks, match items, rubric and
// media, and fresh gap ids, so that a later edit of either leaves the other
// unchanged. Reader is the scope that must be able to read every source
// (questionAccess, a share included); one it cannot read answers as a missing
// one does. Every copy is audited as question.created and, with AuditEach,
// as share.copied from its source; a copy made as part of a test or a set
// leaves that row to the test or set.
type Copy struct {
	IDs       []string
	Reader    access.Scope
	OwnerID   string
	ActorID   string
	AuditEach bool
	Now       time.Time
	IP        string
	UserAgent string
}

// SetCopy is what copyQuestionSet made: the new set, nil when only some of
// the set's questions were copied, and the new questions in set order.
type SetCopy struct {
	SetID       *string
	QuestionIDs []string
}
