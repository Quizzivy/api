//go:build integration

package application_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"quizzivy/internal/core/adapters"
	questionscommand "quizzivy/internal/modules/questions/application/command"
	questionsdomain "quizzivy/internal/modules/questions/domain"
	questionsrepo "quizzivy/internal/modules/questions/repositories"
	"quizzivy/internal/modules/tests/domain"
)

// forkRaceWorld is coEditWorld's shared test T1 with the question writes
// editTestQuestion makes, A's second draft T2, and A's set S1, all through the
// pool, so each write is its own transaction and two of them can contend.
type forkRaceWorld struct {
	*coEditWorld
	t1Section, t2Section string
	set                  string
}

func newForkRaceWorld(t *testing.T) *forkRaceWorld {
	t.Helper()
	ctx := context.Background()
	w := &forkRaceWorld{coEditWorld: newCoEditWorld(t)}
	w.tests.WithQuestionWrites(adapters.QuestionWrites{})
	t.Cleanup(func() {
		if _, err := w.pool.Exec(context.Background(), `DELETE FROM app.question_sets WHERE owner_id = $1`, w.a); err != nil {
			t.Errorf("removing A's set: %v", err)
		}
	})
	if err := w.pool.QueryRow(ctx, `SELECT id::text FROM app.test_sections WHERE test_id = $1`, w.testA).Scan(&w.t1Section); err != nil {
		t.Fatal(err)
	}
	var t2 string
	if err := w.pool.QueryRow(ctx, `INSERT INTO app.tests (title, created_by, owner_id) VALUES ('T2 của A', $1, $1) RETURNING id::text`, w.a).Scan(&t2); err != nil {
		t.Fatal(err)
	}
	if err := w.pool.QueryRow(ctx, `INSERT INTO app.test_sections (test_id, ordinal, title) VALUES ($1, 0, 'Phần 1') RETURNING id::text`, t2).Scan(&w.t2Section); err != nil {
		t.Fatal(err)
	}
	if err := w.pool.QueryRow(ctx, `INSERT INTO app.question_sets (owner_id, name, created_by) VALUES ($1, 'S1 của A', $1) RETURNING id::text`, w.a).Scan(&w.set); err != nil {
		t.Fatal(err)
	}
	return w
}

// fresh is a new question of A's that T1's draft holds as its only question.
func (w *forkRaceWorld) fresh(t *testing.T, prompt string) string {
	t.Helper()
	ctx := context.Background()
	var id string
	if err := w.pool.QueryRow(ctx, `INSERT INTO app.questions (type, prompt, points, created_by, owner_id, tags)
		VALUES ('short_answer', $2, 1, $1, $1, '{}') RETURNING id::text`, w.a, prompt).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if _, err := w.pool.Exec(ctx, `DELETE FROM app.test_section_questions WHERE test_section_id = $1`, w.t1Section); err != nil {
		t.Fatal(err)
	}
	if _, err := w.pool.Exec(ctx, `INSERT INTO app.test_section_questions (test_section_id, ordinal, question_id) VALUES ($1, 0, $2)`, w.t1Section, id); err != nil {
		t.Fatal(err)
	}
	return id
}

func (w *forkRaceWorld) editByB(questionID, prompt string) (domain.QuestionEdited, error) {
	return w.tests.EditQuestion(context.Background(), domain.QuestionEdit{
		Request:    domain.Request{ID: w.testA, ActorID: w.b, Scope: w.scopeB},
		QuestionID: questionID,
		Input:      questionsdomain.Input{Type: questionsdomain.ShortAnswer, Prompt: prompt, Points: "1.00", Tags: []string{}},
		Now:        time.Now(),
	})
}

// deletedInADraft counts the draft rows, of any test, that name a deleted
// question: the invariant LockForDraftUse exists for.
func deletedInADraft(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM app.test_section_questions sq
		JOIN app.questions q ON q.id = sq.question_id WHERE q.deleted_at IS NOT NULL`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestAForkInASharedTestSerialisesAgainstTheOwnersDelete drives B's edit of Q
// in T1, which forks because A's set S1 also holds Q, and A's delete of Q at
// once, as TestLockForDraftUseSerialisesAgainstDelete drives a claim and a
// delete. Both take Q's row lock, so one waits for the other: the delete is
// refused while T1 still names Q, or succeeds after T1 names the fork. No draft
// ever points at a deleted question, and B's edit never fails.
func TestAForkInASharedTestSerialisesAgainstTheOwnersDelete(t *testing.T) {
	w := newForkRaceWorld(t)
	ctx := context.Background()
	for attempt := range 8 {
		q := w.fresh(t, "Câu tranh chấp")
		if _, err := w.pool.Exec(ctx, `INSERT INTO app.question_set_items (set_id, question_id, ordinal, added_by) VALUES ($1, $2, $3, $4)`,
			w.set, q, attempt, w.a); err != nil {
			t.Fatal(err)
		}

		var wg sync.WaitGroup
		var deleteErr, editErr error
		var edited domain.QuestionEdited
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, deleteErr = w.questions.Commands.Delete.Handle(ctx, questionscommand.Delete{Request: questionsdomain.WriteRequest{ID: q, ActorID: w.a}})
		}()
		go func() {
			defer wg.Done()
			edited, editErr = w.editByB(q, "B sửa khi A xoá")
		}()
		wg.Wait()

		if n := deletedInADraft(t, w.pool); n != 0 {
			t.Fatalf("attempt %d: %d draft rows name a deleted question (delete=%v, edit=%v)", attempt, n, deleteErr, editErr)
		}
		if editErr != nil {
			t.Fatalf("attempt %d: B's edit failed with %v; the delete cannot take Q from T1", attempt, editErr)
		}
		if !edited.Forked {
			t.Fatalf("attempt %d: B's edit was made in place although S1 held Q when it began", attempt)
		}
		if deleteErr != nil && !errors.Is(deleteErr, questionsdomain.ErrReferenced) {
			t.Errorf("attempt %d: the delete failed with %v, want success or ErrReferenced", attempt, deleteErr)
		}
		var holds string
		if err := w.pool.QueryRow(ctx, `SELECT question_id::text FROM app.test_section_questions WHERE test_section_id = $1`, w.t1Section).Scan(&holds); err != nil {
			t.Fatal(err)
		}
		if holds != edited.Question.ID {
			t.Errorf("attempt %d: T1 holds %s, want the fork %s", attempt, holds, edited.Question.ID)
		}
	}
}

// TestAnEditDecidesItsForkUnderTheQuestionsLock: A puts Q into T2's draft in
// a transaction that holds Q's draft lock, as autosave does, while B's edit of
// Q in T1 starts. B's edit must wait for that lock before it counts what uses
// Q, so it sees T2 and forks: T2 keeps Q as A wrote it. Counted before the
// lock, B would see T1 alone, edit Q in place once A commits, and silently
// change T2, a test never shared with B. Each attempt makes the interleaving
// happen by holding A's transaction open until B is blocked on it, so without
// the lock the first attempt fails.
func TestAnEditDecidesItsForkUnderTheQuestionsLock(t *testing.T) {
	w := newForkRaceWorld(t)
	ctx := context.Background()
	for attempt := range 2 {
		q := w.fresh(t, "Câu A sắp thêm vào T2")
		if _, err := w.pool.Exec(ctx, `DELETE FROM app.test_section_questions WHERE test_section_id = $1`, w.t2Section); err != nil {
			t.Fatal(err)
		}

		tx, err := w.pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var holder int
		if err := tx.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&holder); err != nil {
			t.Fatal(err)
		}
		if err := questionsrepo.LockForDraftUse(ctx, tx, q); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO app.test_section_questions (test_section_id, ordinal, question_id) VALUES ($1, 0, $2)`, w.t2Section, q); err != nil {
			t.Fatal(err)
		}

		done := make(chan struct{})
		var edited domain.QuestionEdited
		var editErr error
		go func() {
			defer close(done)
			edited, editErr = w.editByB(q, "B sửa trong T1")
		}()
		waitUntilBlockedBy(t, w.pool, holder, done)
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		<-done

		if editErr != nil {
			t.Fatalf("attempt %d: B's edit: %v", attempt, editErr)
		}
		var prompt string
		if err := w.pool.QueryRow(ctx, `SELECT q.prompt FROM app.test_section_questions sq JOIN app.questions q ON q.id = sq.question_id
			WHERE sq.test_section_id = $1`, w.t2Section).Scan(&prompt); err != nil {
			t.Fatal(err)
		}
		if !edited.Forked || prompt != "Câu A sắp thêm vào T2" {
			t.Fatalf("attempt %d: B's edit forked=%v and T2's question now reads %q: an edit grant on T1 changed T2", attempt, edited.Forked, prompt)
		}
	}
}

// waitUntilBlockedBy returns once some session waits on a lock the backend
// holder holds, and fails the test if done closes first or nothing waits
// within five seconds.
func waitUntilBlockedBy(t *testing.T, pool *pgxpool.Pool, holder int, done <-chan struct{}) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-done:
			t.Fatal("B's edit finished while A's transaction still held the question")
		default:
		}
		var blocked bool
		if err := pool.QueryRow(context.Background(),
			`SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE $1::int = ANY(pg_blocking_pids(pid)))`, holder).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("B's edit never waited on A's transaction")
}
