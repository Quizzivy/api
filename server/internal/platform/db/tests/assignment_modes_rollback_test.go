//go:build integration

package db_test

import (
	"database/sql"
	"os"
	"testing"
	"time"

	"github.com/pressly/goose/v3"

	"quizzivy/internal/platform/db"
)

// F1's migration (00174), and the version before it.
const (
	beforeAssignmentModes int64 = 173
	assignmentModes       int64 = 174
)

const assignmentModesRefusal = "a flexible assignment exists; dropping mode would lose its rule"

// TestTheAssignmentModesRollbackRefusesWhileAFlexibleAssignmentExists walks
// 00174's Down past a flexible assignment: it refuses with the Down's own
// sentence, since v0.14.0 would read the row as strict, and runs once the
// assignment is strict again, with a strict one kept through it.
func TestTheAssignmentModesRollbackRefusesWhileAFlexibleAssignmentExists(t *testing.T) {
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
	if err := goose.UpTo(conn, dir, assignmentModes); err != nil {
		t.Fatal(err)
	}

	var flexible string
	withTx(t, conn, func(tx *sql.Tx, f fixture) {
		section := newVersion(t, tx, f.adminID)
		now := time.Now()
		insert := `
			INSERT INTO app.assignments
			       (test_id, test_version_id, opens_at, closes_at, duration_minutes, max_attempts,
			        created_by, published_at, mode)
			SELECT v.test_id, v.id, $2, $3, 45, 1, $4, $2, $5::app.assignment_mode
			  FROM app.test_versions v JOIN app.test_version_sections s ON s.test_version_id = v.id
			 WHERE s.id = $1
			RETURNING id`
		if err := tx.QueryRow(insert, section, now.Add(-time.Hour), now.Add(time.Hour), f.adminID, "flexible").Scan(&flexible); err != nil {
			t.Fatal(err)
		}
		var strict string
		if err := tx.QueryRow(insert, section, now.Add(-time.Hour), now.Add(time.Hour), f.adminID, "strict").Scan(&strict); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	})

	walkRefusalsSaying(t, conn, dir, assignmentModesRefusal, []refusal{
		{"a flexible assignment", beforeAssignmentModes, assignmentModes, func() {
			if _, err := conn.Exec(`UPDATE app.assignments SET mode = 'strict' WHERE id = $1`, flexible); err != nil {
				t.Fatal(err)
			}
		}},
	})
	if err := goose.DownTo(conn, dir, beforeAssignmentModes); err != nil {
		t.Fatalf("the rollback with no flexible assignment left: %v", err)
	}
	if err := goose.UpTo(conn, dir, assignmentModes); err != nil {
		t.Fatal(err)
	}
}
