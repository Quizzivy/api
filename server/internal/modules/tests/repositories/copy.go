package repositories

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	questions "quizzivy/internal/modules/questions/domain"
	"quizzivy/internal/modules/tests/domain"
	"quizzivy/internal/shared/access"
	"quizzivy/internal/shared/audit"
	"quizzivy/internal/shared/opt"
)

// Copy is "Make a copy" (copyTest, D4, T-R7.7): the current draft of the test
// in.ID, which in.Scope must be able to read, becomes a new test owned by the
// actor, independent of its source. In one transaction:
//
//  1. the source's row under a share lock (reachTest for a read), then its
//     draft's groups and questions under share locks, as publish takes them;
//  2. a new test of the actor's, titled and described as the source, as
//     Duplicate names its copies;
//  3. every standalone question of the draft as a new bank question of the
//     actor's, through the questions module's copy (QuestionWrites), which
//     locks each source and then each asset it names with
//     media.LockForVersionUse before writing the copy, and copies its
//     options, blanks, match items and rubric with fresh gap ids;
//  4. the sections, their instructions and the test's own groups, as
//     Duplicate writes them, with each copy locked with
//     questions.LockForDraftUse before test_section_questions names it;
//  5. share.copied for the new test, from the source.
//
// Versions, assignments and grants stay behind. Duplicate is the owner's: it
// keeps referencing the bank questions. A test the scope cannot read is
// domain.ErrNotFound, as a missing one is.
func (s *Postgres) Copy(ctx context.Context, in domain.DuplicateInput) (domain.Test, error) {
	if s.writes == nil || s.questions == nil {
		return domain.Test{}, errQuestionWritesUnavailable
	}
	tx, err := s.Begin(ctx)
	if err != nil {
		return domain.Test{}, fmt.Errorf("tests: begin copy: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := reachTest(ctx, tx, in.Scope, in.ID, domain.ActionRead, testShared); err != nil {
		return domain.Test{}, err
	}
	source, err := s.get(ctx, tx, in.ID)
	if err != nil {
		return domain.Test{}, err
	}
	var copyID string
	if err := tx.QueryRow(ctx,
		`INSERT INTO app.tests (title, description, created_by, updated_by, owner_id)
		 SELECT title, description, $2, $2, $2 FROM app.tests WHERE id = $1
		 RETURNING id::text`, in.ID, in.ActorID).Scan(&copyID); err != nil {
		return domain.Test{}, fmt.Errorf("tests: copy test row: %w", err)
	}
	if len(source.Sections) > 0 {
		if err := lockDraftContent(ctx, tx, in.ID, false); err != nil {
			return domain.Test{}, err
		}
		draft, err := s.loadDraftSnapshot(ctx, tx, in.ID)
		if err != nil {
			return domain.Test{}, err
		}
		copyOf, err := s.copyDraftQuestions(ctx, tx, draft, in)
		if err != nil {
			return domain.Test{}, err
		}
		if err := s.copyDraftGraph(ctx, tx, copyID, in.ActorID, draft, in, copyOf); err != nil {
			return domain.Test{}, err
		}
	}
	diff, err := json.Marshal(map[string]string{"sourceId": in.ID})
	if err != nil {
		return domain.Test{}, err
	}
	if err := audit.Write(ctx, tx, audit.Entry{
		ActorUserID: &in.ActorID,
		Action:      "share.copied",
		Entity:      entityTest,
		EntityID:    &copyID,
		OccurredAt:  in.Now,
		IP:          opt.String(in.IP),
		UserAgent:   opt.String(in.UserAgent),
		Diff:        diff,
	}); err != nil {
		return domain.Test{}, err
	}
	created, err := s.get(ctx, tx, copyID)
	if err != nil {
		return domain.Test{}, err
	}
	readerView(&created, domain.AccessOwner)
	if err := tx.Commit(ctx); err != nil {
		return domain.Test{}, fmt.Errorf("tests: commit copy: %w", err)
	}
	return created, nil
}

// copyDraftQuestions copies every standalone question of draft, each once
// however many sections name it, as a new bank question of the actor's, and
// answers which copy stands for which source. Reading the test authorized
// reading its draft, so the sources are read as the draft holds them, every
// one of them.
func (s *Postgres) copyDraftQuestions(ctx context.Context, tx pgx.Tx, draft domain.DraftContent, in domain.DuplicateInput) (func(string) string, error) {
	var sources []string
	for _, section := range draft.Sections {
		sources = append(sources, standaloneDraftIDs(section)...)
	}
	copies, err := s.writes.CopyQuestions(ctx, tx, questions.Copy{IDs: sources, Reader: draftReader, OwnerID: in.ActorID, ActorID: in.ActorID,
		Now: in.Now, IP: in.IP, UserAgent: in.UserAgent})
	if err != nil {
		return nil, copyFailure(err)
	}
	bySource := make(map[string]string, len(sources))
	for i, source := range sources {
		bySource[strings.ToLower(source)] = copies[i]
	}
	return func(source string) string { return bySource[strings.ToLower(source)] }, nil
}

// draftReader reads every question: the questions of a draft whose test the
// copier was allowed to read.
var draftReader = access.Scope{All: true}

// CopyQuestions is copyQuestionsForTest (T-R7.7): copies of in.QuestionIDs,
// each of which in.Scope must be able to read, made in the bank of the owner
// of the test in.ID, which in.Scope must be able to edit, created by the
// actor and audited each as share.copied from its source. The test's row is
// held under a share lock while the questions module copies them, so the
// test cannot change hands before they land in its owner's bank; nothing of
// the test itself changes, and the builder names the copies in its next
// autosave, which takes LockForDraftUse on each. A test the scope cannot read
// is domain.ErrNotFound and a Can use grant's domain.ErrReadOnly, before any
// question is read; a question it cannot read is the questions module's
// QuestionsNotFoundError naming it.
func (s *Postgres) CopyQuestions(ctx context.Context, in domain.QuestionCopies) ([]string, error) {
	if s.writes == nil {
		return nil, errQuestionWritesUnavailable
	}
	tx, err := s.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("tests: begin question copies: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	reached, err := reachTest(ctx, tx, in.Scope, in.ID, domain.ActionEdit, testShared)
	if err != nil {
		return nil, err
	}
	copies, err := s.writes.CopyQuestions(ctx, tx, questions.Copy{IDs: in.QuestionIDs, Reader: in.Scope, OwnerID: reached.owner,
		ActorID: in.ActorID, AuditEach: true, Now: in.Now, IP: in.IP, UserAgent: in.UserAgent})
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("tests: commit question copies: %w", err)
	}
	return copies, nil
}

// copyFailure is a copy's refusal of a draft's question as this module
// answers it: a question gone since the draft was read as one the outline
// cannot name, anything else as the questions module raised it.
func copyFailure(err error) error {
	var missing *questions.QuestionsNotFoundError
	if errors.As(err, &missing) {
		return fmt.Errorf("%w: %s", domain.ErrUnknownQuestion, strings.Join(missing.IDs, ", "))
	}
	return err
}
