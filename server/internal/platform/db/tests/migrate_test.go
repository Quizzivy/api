package db_test

import (
	"database/sql"
	"net/url"
	"os"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"quizzivy/internal/platform/db"
)

// §13.7 requires every migration to have a Down that actually works. This is
// what makes that true rather than aspirational: up -> down -> up must leave a
// byte-identical schema, and the intermediate state must be genuinely empty.
//
// An unreversible migration is cheap to fix on the day it is written and
// expensive months later, when the only way to test a change is against
// production-shaped data.

func openMigrate(t *testing.T) *sql.DB {
	t.Helper()
	conn, err := sql.Open("pgx", db.TestDSN(t))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := conn.Ping(); err != nil {
		t.Fatalf("ping: %v", err)
	}
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatalf("dialect: %v", err)
	}
	goose.SetLogger(goose.NopLogger())
	return conn
}

// schemaSnapshot captures everything migrations create in `app`, ordered
// deterministically so two runs are comparable as strings.
func schemaSnapshot(t *testing.T, conn *sql.DB) string {
	t.Helper()
	const q = `
WITH types AS (
  SELECT 'type ' || t.typname || ' = ' ||
         coalesce((SELECT string_agg(e.enumlabel, ',' ORDER BY e.enumsortorder)
                   FROM pg_enum e WHERE e.enumtypid = t.oid), '') AS line
  FROM pg_type t JOIN pg_namespace n ON n.oid = t.typnamespace
  WHERE n.nspname = 'app' AND t.typtype = 'e'
),
funcs AS (
  SELECT 'func ' || p.proname || '(' || pg_get_function_identity_arguments(p.oid) || ') ' ||
         CASE p.provolatile WHEN 'i' THEN 'immutable' WHEN 's' THEN 'stable' ELSE 'volatile' END AS line
  FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
  WHERE n.nspname = 'app'
),
tables AS (
  SELECT 'table ' || c.relname || '(' ||
         (SELECT string_agg(a.attname || ':' || format_type(a.atttypid, a.atttypmod) ||
                            CASE WHEN a.attnotnull THEN ' NOT NULL' ELSE '' END, ', '
                            ORDER BY a.attnum)
          FROM pg_attribute a WHERE a.attrelid = c.oid AND a.attnum > 0 AND NOT a.attisdropped)
         || ')' AS line
  FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
  WHERE n.nspname = 'app' AND c.relkind = 'r'
),
indexes AS (
  SELECT 'index ' || indexname || ' = ' || indexdef AS line
  FROM pg_indexes WHERE schemaname = 'app'
),
constraints AS (
  SELECT 'constraint ' || c.conname || ' = ' || pg_get_constraintdef(c.oid) AS line
  FROM pg_constraint c JOIN pg_namespace n ON n.oid = c.connamespace
  WHERE n.nspname = 'app'
)
SELECT coalesce(string_agg(line, E'\n' ORDER BY line), '')
FROM (
  SELECT line FROM types
  UNION ALL SELECT line FROM funcs
  UNION ALL SELECT line FROM tables
  UNION ALL SELECT line FROM indexes
  UNION ALL SELECT line FROM constraints
) all_objects;`

	var snapshot string
	if err := conn.QueryRow(q).Scan(&snapshot); err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	return snapshot
}

// scratchDatabase creates a database of its own for the destructive round trip
// and drops it afterwards.
func scratchDatabase(t *testing.T) string {
	t.Helper()
	admin, err := sql.Open("pgx", db.TestDSN(t))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = admin.Close() }()
	if err := admin.Ping(); err != nil {
		t.Fatalf("ping: %v", err)
	}
	const name = "quizzivy_migrate_check"
	if _, err := admin.Exec(`DROP DATABASE IF EXISTS ` + name); err != nil {
		t.Fatalf("dropping a leftover %s: %v", name, err)
	}
	if _, err := admin.Exec(`CREATE DATABASE ` + name); err != nil {
		t.Fatalf("creating %s: %v\n\n"+
			"The migrate role needs CREATEDB for this test. Re-run provisioning\n"+
			"(`make db-provision`, or ALTER ROLE quizzivy_migrate CREATEDB).", name, err)
	}
	t.Cleanup(func() {
		cleanup, err := sql.Open("pgx", db.TestDSN(t))
		if err != nil {
			return
		}
		defer func() { _ = cleanup.Close() }()
		_, _ = cleanup.Exec(`DROP DATABASE IF EXISTS ` + name)
	})

	return swapDatabase(t, db.TestDSN(t), name)
}

// swapDatabase rewrites the database name in a DSN, leaving everything else --
// credentials, host, sslmode -- exactly as configured.
func swapDatabase(t *testing.T, dsn, name string) string {
	t.Helper()
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("TEST_DATABASE_URL is not a URL: %v", err)
	}
	u.Path = "/" + name
	return u.String()
}

func TestMigrationsAreReversible(t *testing.T) {
	if os.Getenv("TEST_DESTRUCTIVE") != "1" {
		t.Skip("TEST_DESTRUCTIVE=1 not set; skipping the destructive migration round trip " +
			"(it creates and drops a scratch database)")
	}

	conn, err := sql.Open("pgx", scratchDatabase(t))
	if err != nil {
		t.Fatalf("open scratch: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := conn.Ping(); err != nil {
		t.Fatalf("ping scratch: %v", err)
	}
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatalf("dialect: %v", err)
	}
	goose.SetLogger(goose.NopLogger())

	dir := db.MigrationsDir(t)

	if err := goose.Up(conn, dir); err != nil {
		t.Fatalf("first up: %v", err)
	}
	before := schemaSnapshot(t, conn)
	if before == "" {
		t.Fatal("snapshot is empty after `up`; the query or the migrations are wrong")
	}

	if err := goose.Reset(conn, dir); err != nil {
		t.Fatalf("reset: %v", err)
	}
	empty := schemaSnapshot(t, conn)
	if empty != "" {
		t.Errorf("`down` left objects behind in schema app:\n%s", empty)
	}
	var schemas int
	if err := conn.QueryRow(
		`SELECT count(*) FROM information_schema.schemata WHERE schema_name = 'app'`,
	).Scan(&schemas); err != nil {
		t.Fatalf("schema check: %v", err)
	}
	if schemas != 0 {
		t.Error("schema app survived `down`")
	}

	if err := goose.Up(conn, dir); err != nil {
		t.Fatalf("second up: %v", err)
	}
	after := schemaSnapshot(t, conn)

	if before != after {
		t.Errorf("schema differs after up -> down -> up\n--- before ---\n%s\n--- after ---\n%s", before, after)
	}
}

func TestFoundationObjectsExist(t *testing.T) {
	conn := openMigrate(t)
	if err := goose.Up(conn, db.MigrationsDir(t)); err != nil {
		t.Fatalf("up: %v", err)
	}

	var enums int
	if err := conn.QueryRow(`
		SELECT count(*) FROM pg_type t JOIN pg_namespace n ON n.oid = t.typnamespace
		WHERE n.nspname = 'app' AND t.typtype = 'e'`).Scan(&enums); err != nil {
		t.Fatal(err)
	}
	if enums != 6 {
		t.Errorf("found %d enum types in app, want 6 (§13.2 plus D-15, less user_role since T-R3.1)", enums)
	}

	for _, fn := range []string{"immutable_unaccent", "set_updated_at"} {
		var n int
		if err := conn.QueryRow(`
			SELECT count(*) FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
			WHERE n.nspname = 'app' AND p.proname = $1`, fn).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			t.Errorf("app.%s() is missing", fn)
		}
	}
}

// The sharing files (00159 to 00161, T-R7.1), and the version before them.
const (
	beforeSharing int64 = 158
	sharing       int64 = 161
)

// TestTheSharingFilesApplyOverPopulatedTablesAndRollBack runs 00159 to 00161
// over a test and a question that already exist, as production's do, then
// walks them down and up again: the columns arrive NULL, the Down removes the
// tables and the columns, and a second Up restores them.
func TestTheSharingFilesApplyOverPopulatedTablesAndRollBack(t *testing.T) {
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
	if err := goose.UpTo(conn, dir, beforeSharing); err != nil {
		t.Fatal(err)
	}
	withTx(t, conn, func(tx *sql.Tx, f fixture) {
		newDraftTest(t, tx, f.adminID)
		newQuestion(t, tx, f.adminID, "short_answer", "Câu có trước R7")
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	})

	present := func() (tables, columns int) {
		t.Helper()
		if err := conn.QueryRow(`SELECT
			(SELECT count(*) FROM information_schema.tables WHERE table_schema = 'app'
			    AND table_name IN ('question_sets', 'question_set_items', 'content_shares')),
			(SELECT count(*) FROM information_schema.columns WHERE table_schema = 'app'
			    AND table_name IN ('tests', 'questions') AND column_name = 'updated_by')`).Scan(&tables, &columns); err != nil {
			t.Fatal(err)
		}
		return tables, columns
	}
	if err := goose.UpTo(conn, dir, sharing); err != nil {
		t.Fatalf("up over populated tables: %v", err)
	}
	if tables, columns := present(); tables != 3 || columns != 2 {
		t.Fatalf("after up: %d tables and %d columns, want 3 and 2", tables, columns)
	}
	var filled int
	if err := conn.QueryRow(`SELECT (SELECT count(*) FROM app.tests WHERE updated_by IS NOT NULL)
		+ (SELECT count(*) FROM app.questions WHERE updated_by IS NOT NULL)`).Scan(&filled); err != nil {
		t.Fatal(err)
	}
	if filled != 0 {
		t.Errorf("%d existing rows gained an updated_by", filled)
	}
	if err := goose.DownTo(conn, dir, beforeSharing); err != nil {
		t.Fatalf("down: %v", err)
	}
	if tables, columns := present(); tables != 0 || columns != 0 {
		t.Fatalf("after down: %d tables and %d columns left", tables, columns)
	}
	if err := goose.UpTo(conn, dir, sharing); err != nil {
		t.Fatalf("second up: %v", err)
	}
	if tables, columns := present(); tables != 3 || columns != 2 {
		t.Errorf("after the second up: %d tables and %d columns, want 3 and 2", tables, columns)
	}
}
