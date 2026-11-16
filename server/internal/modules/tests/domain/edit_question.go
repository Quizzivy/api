package domain

import (
	"time"

	questions "quizzivy/internal/modules/questions/domain"
)

// QuestionEdit is an edit of the bank question QuestionID made in the context
// of the test Request.ID (editTestQuestion, T-R7.6): the question's new
// content, checked against its revision ExpectedUpdatedAt when that is set.
// The question must be a standalone question of the test's draft.
type QuestionEdit struct {
	Request
	QuestionID        string
	Input             questions.Input
	ExpectedUpdatedAt *time.Time
	Now               time.Time
}

// QuestionEdited is a QuestionEdit as saved: the question, the copy when the
// edit forked the original (copy-on-write), and the test's revision after it.
type QuestionEdited struct {
	Question      questions.Question
	Forked        bool
	TestUpdatedAt time.Time
}
