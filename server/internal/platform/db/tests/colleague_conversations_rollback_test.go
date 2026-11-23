//go:build integration

package db_test

import (
	"database/sql"
	"os"
	"testing"

	"github.com/pressly/goose/v3"

	"quizzivy/internal/platform/db"
)

// F2's migration (00180, T-F2.2), and the version before it.
const (
	beforeColleagueConversations int64 = 179
	colleagueConversations       int64 = 180
)

const colleagueConversationsRefusal = "a colleague conversation exists; dropping its columns would lose the thread"

// TestTheColleagueConversationsFileAppliesOverThreadsAndRollsBack runs 00180
// over a direct thread and a class channel that already exist, as
// production's do, then stores a colleague thread. Down refuses with its own
// sentence while that thread exists, since v0.15.0 cannot read one, and runs
// once it is gone: the schema is exactly 00179's again (the two-kind check,
// no pair columns, checks or indexes), quizzivy_maintenance holds on
// app.conversations what it held before, and the two old rows are kept. Then
// up runs a second time.
func TestTheColleagueConversationsFileAppliesOverThreadsAndRollsBack(t *testing.T) {
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
	if err := goose.UpTo(conn, dir, beforeColleagueConversations); err != nil {
		t.Fatal(err)
	}
	maintenanceHolds := func() (columns string) {
		t.Helper()
		if err := conn.QueryRow(`SELECT coalesce(string_agg(a.attname, ',' ORDER BY a.attname), '') FROM pg_attribute a
			 WHERE a.attrelid = 'app.conversations'::regclass AND a.attnum > 0 AND NOT a.attisdropped
			   AND has_column_privilege($1, a.attrelid, a.attnum, 'SELECT')`, maintenanceRole).Scan(&columns); err != nil {
			t.Fatal(err)
		}
		return columns
	}
	before, held := schemaSnapshot(t, conn), maintenanceHolds()

	var admin string
	withTx(t, conn, func(tx *sql.Tx, f fixture) {
		admin = f.adminID
		newDirect(t, tx, f.adminID, f.studentID)
		mustExec(t, tx, `INSERT INTO app.conversations (kind, class_id, last_message_at) VALUES ('class', $1, now())`, f.classID)
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	})
	if err := goose.UpTo(conn, dir, colleagueConversations); err != nil {
		t.Fatalf("up over existing threads: %v", err)
	}
	var colleague string
	withTx(t, conn, func(tx *sql.Tx, _ fixture) {
		low, high := newColleague(t, tx, admin)
		if err := tx.QueryRow(`INSERT INTO app.conversations (kind, colleague_low_id, colleague_high_id, last_message_at)
			VALUES ('colleague', $1, $2, now()) RETURNING id`, low, high).Scan(&colleague); err != nil {
			t.Fatalf("after up a colleague thread does not land: %v", err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	})

	walkRefusalsSaying(t, conn, dir, colleagueConversationsRefusal, []refusal{
		{"a colleague conversation", beforeColleagueConversations, colleagueConversations, func() {
			if _, err := conn.Exec(`DELETE FROM app.conversations WHERE id = $1`, colleague); err != nil {
				t.Fatal(err)
			}
		}},
	})
	if err := goose.DownTo(conn, dir, beforeColleagueConversations); err != nil {
		t.Fatalf("the rollback with no colleague thread left: %v", err)
	}
	if after := schemaSnapshot(t, conn); after != before {
		t.Errorf("the schema after down is not 00179's\n--- before ---\n%s\n--- after ---\n%s", before, after)
	}
	if after := maintenanceHolds(); after != held {
		t.Errorf("after down %s reads app.conversations(%s), want (%s)", maintenanceRole, after, held)
	}
	var kept int
	if err := conn.QueryRow(`SELECT count(*) FROM app.conversations WHERE kind IN ('direct', 'class')
		AND (staff_id = $1 OR class_id IN (SELECT id FROM app.classes WHERE teacher_id = $1))`, admin).Scan(&kept); err != nil || kept != 2 {
		t.Errorf("after down %d of the direct thread and the channel are left (%v), want both", kept, err)
	}
	if err := goose.UpTo(conn, dir, colleagueConversations); err != nil {
		t.Fatalf("second up: %v", err)
	}
}
