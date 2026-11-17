package domain

import "time"

// QuestionCopies is copyQuestionsForTest (T-R7.7): copies of the bank
// questions QuestionIDs, each of which the actor must be able to read, made
// in the bank of the owner of the test Request.ID, which the actor must be
// able to edit, with the actor as their creator. A draft holds only questions
// its test's owner owns (T-R2.12a), so this is how a co-editor adds a
// question from their own bank or a shared set: the builder then names the
// copies in its next autosave.
type QuestionCopies struct {
	Request
	QuestionIDs []string
	Now         time.Time
}
