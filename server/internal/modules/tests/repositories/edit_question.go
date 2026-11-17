package repositories

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	questions "quizzivy/internal/modules/questions/domain"
	"quizzivy/internal/modules/tests/domain"
)

// QuestionWrites is the questions module's side of the bank writes this
// module makes in its own transaction. For an edit made in a test's context,
// ContextUse reads what the copy-on-write rule decides on under the
// question's row lock, and WriteInContext writes the edit in place or as a
// fork (questions' repositories.WriteInContext). For a copy (copyTest,
// copyQuestionsForTest), CopyQuestions makes independent copies of bank
// questions the reader can read, under the sources' row locks and the media's
// (questions' repositories.CopyQuestions).
type QuestionWrites interface {
	ContextUse(ctx context.Context, tx pgx.Tx, questionID string, within questions.EditContext) (questions.QuestionUse, error)
	WriteInContext(ctx context.Context, tx pgx.Tx, in questions.WriteInput, use questions.QuestionUse, within questions.EditContext) (questions.Question, bool, error)
	CopyQuestions(ctx context.Context, tx pgx.Tx, in questions.Copy) ([]string, error)
}

// WithQuestionWrites supplies the question writes editTestQuestion and the
// copies make in the test's transaction. Without them EditQuestion, Copy and
// CopyQuestions are unavailable.
func (s *Postgres) WithQuestionWrites(writes QuestionWrites) *Postgres {
	s.writes = writes
	return s
}

var errQuestionWritesUnavailable = errors.New("tests: question writes unavailable")

// EditQuestion edits the standalone question in.QuestionID of the test in.ID's
// draft in the test's context (copy-on-write, D4, T-R7.6), in one transaction
// whose locks go test first, then question, the order autosave takes:
//
//  1. the test's row, for an edit of its draft (reachTest, as autosave);
//  2. questions.LockForDraftUse on the question;
//  3. what uses the question besides this test (ContextUse);
//  4. the edit, in place or, when the rule forks it, as a copy owned by the
//     question's owner and created by the editor;
//  5. on a fork, questions.LockForDraftUse on the copy, then this test's
//     draft rows pointed from the original to it;
//  6. the test's updated_at and updated_by.
//
// The questions module audits the edit, as question.updated in place and as
// question.created and question.forked on a fork.
//
// A test the caller cannot read is domain.ErrNotFound, a Can use grant's
// domain.ErrReadOnly, an archived test domain.ErrArchived, and a question the
// draft does not hold as a standalone question domain.ErrQuestionNotInDraft,
// in that order, so nothing about the question is told to a caller who may
// not edit the test.
func (s *Postgres) EditQuestion(ctx context.Context, in domain.QuestionEdit) (domain.QuestionEdited, error) {
	if s.writes == nil || s.questions == nil {
		return domain.QuestionEdited{}, errQuestionWritesUnavailable
	}
	tx, err := s.Begin(ctx)
	if err != nil {
		return domain.QuestionEdited{}, fmt.Errorf("tests: begin question edit: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := s.lockEditedQuestion(ctx, tx, in); err != nil {
		return domain.QuestionEdited{}, err
	}
	within := questions.EditContext{Kind: questions.ContextTest, ID: in.ID}
	use, err := s.writes.ContextUse(ctx, tx, in.QuestionID, within)
	if err != nil {
		return domain.QuestionEdited{}, questionEditFailure(err)
	}
	written, forked, err := s.writes.WriteInContext(ctx, tx, questions.WriteInput{
		ID: in.QuestionID, Input: in.Input, ActorID: in.ActorID, All: in.Scope.All,
		ExpectedUpdatedAt: in.ExpectedUpdatedAt, Now: in.Now, IP: in.IP, UserAgent: in.UserAgent,
	}, use, within)
	if err != nil {
		return domain.QuestionEdited{}, questionEditFailure(err)
	}
	if forked {
		if err := s.repointDraftQuestion(ctx, tx, in.ID, use.ID, written.ID); err != nil {
			return domain.QuestionEdited{}, err
		}
	}
	var edited domain.QuestionEdited
	if err := tx.QueryRow(ctx, `UPDATE app.tests SET updated_at = $2, updated_by = $3::uuid WHERE id = $1::uuid RETURNING updated_at`,
		in.ID, in.Now, in.ActorID).Scan(&edited.TestUpdatedAt); err != nil {
		return domain.QuestionEdited{}, fmt.Errorf("tests: record question edit: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.QuestionEdited{}, fmt.Errorf("tests: commit question edit: %w", err)
	}
	edited.Question, edited.Forked = written, forked
	return edited, nil
}

// lockEditedQuestion takes steps 1 and 2 of EditQuestion: the test's row for
// an edit of its draft, refused for an archived test, then, once the draft is
// known to hold the question, the question's draft lock.
func (s *Postgres) lockEditedQuestion(ctx context.Context, tx pgx.Tx, in domain.QuestionEdit) error {
	reached, err := reachTest(ctx, tx, in.Scope, in.ID, domain.ActionEdit, testExclusive)
	if err != nil {
		return err
	}
	if reached.status == domain.Archived {
		return domain.ErrArchived
	}
	if err := requireDraftQuestion(ctx, tx, in.ID, in.QuestionID); err != nil {
		return err
	}
	return s.lockDraftQuestion(ctx, tx, in.QuestionID)
}

// requireDraftQuestion is nil when the draft of the test testID holds the bank
// question questionID as a standalone question, in a legacy outline or in a
// group-aware one, and domain.ErrQuestionNotInDraft otherwise.
func requireDraftQuestion(ctx context.Context, tx pgx.Tx, testID, questionID string) error {
	var held bool
	err := tx.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM app.test_sections s
		 WHERE s.test_id = $1::uuid
		   AND (EXISTS (SELECT 1 FROM app.test_section_questions sq WHERE sq.test_section_id = s.id AND sq.question_id = $2::uuid)
		     OR EXISTS (SELECT 1 FROM app.test_section_units u WHERE u.test_section_id = s.id AND u.question_id = $2::uuid)))`,
		testID, questionID).Scan(&held)
	if err != nil {
		return fmt.Errorf("tests: draft question: %w", err)
	}
	if !held {
		return domain.ErrQuestionNotInDraft
	}
	return nil
}

// lockDraftQuestion takes questions.LockForDraftUse on the question; a deleted
// or missing one is domain.ErrQuestionNotInDraft.
func (s *Postgres) lockDraftQuestion(ctx context.Context, tx pgx.Tx, questionID string) error {
	err := s.questions.LockForDraftUse(ctx, tx, questionID)
	if errors.Is(err, questions.ErrNotFound) {
		return domain.ErrQuestionNotInDraft
	}
	return err
}

// repointDraftQuestion takes questions.LockForDraftUse on the fork to, then
// points every row of the test testID's draft that names the question from at
// it, in place, so each keeps its section and ordinal: the legacy outline's
// test_section_questions and the group-aware outline's test_section_units
// alike.
func (s *Postgres) repointDraftQuestion(ctx context.Context, tx pgx.Tx, testID, from, to string) error {
	if err := s.lockDraftQuestion(ctx, tx, to); err != nil {
		return err
	}
	for _, table := range []string{"app.test_section_questions", "app.test_section_units"} {
		if _, err := tx.Exec(ctx, `UPDATE `+table+` r SET question_id = $3::uuid
			  FROM app.test_sections s
			 WHERE s.id = r.test_section_id AND s.test_id = $1::uuid AND r.question_id = $2::uuid`,
			testID, from, to); err != nil {
			return fmt.Errorf("tests: repoint draft question: %w", err)
		}
	}
	return nil
}

// questionEditFailure is a question write's refusal as this module answers
// it: a question gone since its lock as one the draft does not hold, anything
// else, a stale revision and a validation refusal included, as the questions
// module raised it.
func questionEditFailure(err error) error {
	if errors.Is(err, questions.ErrNotFound) {
		return domain.ErrQuestionNotInDraft
	}
	return err
}
