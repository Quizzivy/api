//go:build integration

package db_test

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"quizzivy/internal/platform/db"
)

// recoveryEvidence is what scripts/recovery-evidence.sql reports, read by the
// recovery drill of docs/setup/operations.md through scripts/verify-recovery.mjs.
type recoveryEvidence struct {
	Migration int64 `json:"migration"`
	Users     struct {
		Rows   int             `json:"rows"`
		Digest json.RawMessage `json:"digest"`
	} `json:"users"`
}

// evidenceIn runs the script's one query inside tx. The file opens and commits
// a read-only transaction of its own for the drill; here the query has to see
// the rows the test has not committed.
func evidenceIn(t *testing.T, tx *sql.Tx) recoveryEvidence {
	t.Helper()
	script, err := os.ReadFile(filepath.Join(db.MigrationsDir(t), "..", "scripts", "recovery-evidence.sql"))
	if err != nil {
		t.Fatal(err)
	}
	var query []string
	for _, line := range strings.Split(string(script), "\n") {
		if strings.HasPrefix(line, "BEGIN") || strings.HasPrefix(line, "COMMIT") {
			continue
		}
		query = append(query, line)
	}
	var raw []byte
	if err := tx.QueryRow(strings.Join(query, "\n")).Scan(&raw); err != nil {
		t.Fatalf("scripts/recovery-evidence.sql does not run on the migrated schema: %v", err)
	}
	var evidence recoveryEvidence
	if err := json.Unmarshal(raw, &evidence); err != nil {
		t.Fatalf("the evidence is not the object the verifier parses: %v: %s", err, raw)
	}
	return evidence
}

// The query names its columns, so a migration that drops or renames one breaks
// the drill: users.role went in 00105 and nothing ran the script to notice.
func TestTheRecoveryEvidenceReadsTheMigratedSchema(t *testing.T) {
	conn := migrated(t)
	withTx(t, conn, func(tx *sql.Tx, f fixture) {
		before := evidenceIn(t, tx)

		var applied int64
		if err := tx.QueryRow(`SELECT max(version_id) FROM public.goose_db_version WHERE is_applied`).Scan(&applied); err != nil {
			t.Fatal(err)
		}
		if before.Migration != applied {
			t.Errorf("the evidence names migration %d, the database is at %d", before.Migration, applied)
		}
		if before.Users.Rows < 2 {
			t.Errorf("the evidence counts %d users, the fixture alone has two", before.Users.Rows)
		}

		if _, err := tx.Exec(`UPDATE app.users SET role_id = (SELECT id FROM app.roles WHERE builtin_key = 'teacher') WHERE id = $1`, f.studentID); err != nil {
			t.Fatal(err)
		}
		if after := evidenceIn(t, tx); string(after.Users.Digest) == string(before.Users.Digest) {
			t.Error("a user's role changed and the users digest did not: the role is one of the critical columns")
		}
	})
}
