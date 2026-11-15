//go:build integration

package db_test

import (
	"database/sql"
	"os"
	"testing"

	"github.com/pressly/goose/v3"

	"quizzivy/internal/platform/db"
)

// The essay word limit's migration (00158), and the version before it.
const (
	beforeEssayWordLimit int64 = 157
	essayWordLimit       int64 = 158
)

const essayWordLimitRefusal = "an essay with a word limit exists; the short-answer-only rule cannot hold it"

// TestTheEssayWordLimitRollbackRefusesWhileAnEssayHasOne walks 00158's Down
// past an essay with a word limit in the bank, then one frozen in a version:
// each alone refuses it with the Down's own sentence, and the rollback runs
// once neither is left, with a short answer's limit kept through it.
func TestTheEssayWordLimitRollbackRefusesWhileAnEssayHasOne(t *testing.T) {
	if os.Getenv("TEST_DESTRUCTIVE") != "1" {
		t.Skip("TEST_DESTRUCTIVE=1 required for an isolated migration database")
	}
	conn, err := sql.Open("pgx", scratchDatabase(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatal(err)
	}
	goose.SetLogger(goose.NopLogger())
	dir := db.MigrationsDir(t)
	if err := goose.UpTo(conn, dir, essayWordLimit); err != nil {
		t.Fatal(err)
	}

	var essay, frozen string
	withTx(t, conn, func(tx *sql.Tx, f fixture) {
		if err := tx.QueryRow(`INSERT INTO app.questions (type, prompt, points, created_by, owner_id, max_words)
			VALUES ('essay', 'Viết không quá 250 từ', 9.0, $1, $1, 250) RETURNING id`, f.adminID).Scan(&essay); err != nil {
			t.Fatal(err)
		}
		mustExec(t, tx, `INSERT INTO app.questions (type, prompt, points, created_by, owner_id, max_words)
			VALUES ('short_answer', 'Không quá BA từ', 1.0, $1, $1, 3)`, f.adminID)
		section := newVersion(t, tx, f.adminID)
		if err := tx.QueryRow(`INSERT INTO app.test_version_questions (test_version_section_id, ordinal, type, prompt, points, max_words)
			VALUES ($1, 0, 'essay', 'Bài luận đã chốt', 9.0, 400) RETURNING id`, section).Scan(&frozen); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	})

	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := conn.Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	walkRefusalsSaying(t, conn, dir, essayWordLimitRefusal, []refusal{
		{"an essay with a word limit in the bank", beforeEssayWordLimit, essayWordLimit, func() {
			exec(`UPDATE app.questions SET max_words = NULL WHERE id = $1`, essay)
		}},
		{"an essay with a word limit in a version", beforeEssayWordLimit, essayWordLimit, func() {
			exec(`UPDATE app.test_version_questions SET max_words = NULL WHERE id = $1`, frozen)
		}},
	})
	if err := goose.DownTo(conn, dir, beforeEssayWordLimit); err != nil {
		t.Fatalf("the rollback with no essay limit left: %v", err)
	}
	if _, err := conn.Exec(`INSERT INTO app.questions (type, prompt, points, created_by, owner_id, max_words)
		SELECT 'essay', 'Lại có giới hạn', 9.0, created_by, owner_id, 250 FROM app.questions WHERE id = $1`, essay); err == nil {
		t.Error("after the rollback an essay still takes a word limit")
	}
	if err := goose.UpTo(conn, dir, essayWordLimit); err != nil {
		t.Fatal(err)
	}
}
