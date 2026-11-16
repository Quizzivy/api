package db_test

import (
	"database/sql"
	"maps"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

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

// The messaging files (00162 to 00167, T-R7.9): the sixth switch, then the five tables.
const messaging int64 = 167

// TestTheMessagingFilesFillTheTiesAndRollBack runs 00162 to 00167 over a class
// that already has a member and a past: a student who joined with a code and
// left, whose class.member_enrolled row is all that is left of it, and a
// student staff added and removed, whose class.member_added row names the
// class only. The fill ties the class's teacher to the first two, since the
// earliest moment known, and cannot know the third (O-R7.8). Then the files go
// down, taking the tables and a saved message.received switch with them, and
// come up again.
func TestTheMessagingFilesFillTheTiesAndRollBack(t *testing.T) {
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
	if err := goose.UpTo(conn, dir, sharing); err != nil {
		t.Fatal(err)
	}
	var teacher, member, joinedAndLeft, addedAndLeft, class string
	withTx(t, conn, func(tx *sql.Tx, f fixture) {
		teacher, member, class = f.adminID, f.studentID, f.classID
		student := func(name string) string {
			var id string
			if err := tx.QueryRow(`INSERT INTO app.users (email, full_name, role_id)
				VALUES ($1 || '-' || uuidv7() || '@example.com', $1, (SELECT id FROM app.roles WHERE builtin_key = 'student')) RETURNING id`, name).Scan(&id); err != nil {
				t.Fatal(err)
			}
			return id
		}
		joinedAndLeft, addedAndLeft = student("joined"), student("added")
		mustExec(t, tx, `INSERT INTO app.class_members (class_id, user_id, joined_via, joined_at, added_by)
			VALUES ($1, $2, 'admin', '2026-09-01T08:00:00Z', $3)`, class, member, teacher)
		mustExec(t, tx, `INSERT INTO app.audit_log (actor_user_id, action, entity, entity_id, occurred_at, diff) VALUES
			($2::uuid, 'class.member_enrolled', 'class_member', $1::uuid, '2026-08-01T08:00:00Z', jsonb_build_object('class_id', $1::text, 'user_id', $2::text, 'join_code_id', uuidv7()::text)),
			($2::uuid, 'class.member_enrolled', 'class_member', $1::uuid, '2026-08-15T08:00:00Z', jsonb_build_object('class_id', $1::text, 'user_id', $2::text, 'join_code_id', uuidv7()::text)),
			($3::uuid, 'class.member_enrolled', 'class_member', $1::uuid, '2026-07-01T08:00:00Z', jsonb_build_object('class_id', $1::text, 'user_id', $3::text, 'join_code_id', uuidv7()::text)),
			($3::uuid, 'class.member_enrolled', 'class_member', $1::uuid, now(), '{"class_id": "not-a-uuid", "user_id": 7}'),
			($4::uuid, 'class.member_added', 'class_member', $1::uuid, now(), NULL),
			($4::uuid, 'class.member_removed', 'class_member', $1::uuid, now(), NULL)`, class, joinedAndLeft, member, teacher)
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	})

	present := func() (tables int) {
		t.Helper()
		if err := conn.QueryRow(`SELECT count(*) FROM information_schema.tables WHERE table_schema = 'app'
			AND table_name IN ('message_attachments', 'conversations', 'messages', 'conversation_reads', 'messaging_ties')`).Scan(&tables); err != nil {
			t.Fatal(err)
		}
		return tables
	}
	if err := goose.UpTo(conn, dir, messaging); err != nil {
		t.Fatalf("up over populated tables: %v", err)
	}
	if n := present(); n != 5 {
		t.Fatalf("after up: %d tables, want 5", n)
	}
	rows, err := conn.Query(`SELECT student_id::text, since FROM app.messaging_ties WHERE staff_id = $1`, teacher)
	if err != nil {
		t.Fatal(err)
	}
	ties := map[string]time.Time{}
	for rows.Next() {
		var student string
		var since time.Time
		if err := rows.Scan(&student, &since); err != nil {
			t.Fatal(err)
		}
		ties[student] = since
	}
	_ = rows.Close()
	want := map[string]time.Time{
		member:        time.Date(2026, 7, 1, 8, 0, 0, 0, time.UTC),
		joinedAndLeft: time.Date(2026, 8, 1, 8, 0, 0, 0, time.UTC),
	}
	if len(ties) != len(want) {
		t.Errorf("the fill tied the teacher to %v, want the member and the student who joined and left", ties)
	}
	for student, since := range want {
		if got, ok := ties[student]; !ok || !got.Equal(since) {
			t.Errorf("tie to %s since %v (%v), want since %v", student, got, ok, since)
		}
	}
	if _, ok := ties[addedAndLeft]; ok {
		t.Error("a student staff added and removed is tied; their audit rows name no student")
	}
	if _, err := conn.Exec(`INSERT INTO app.notification_preferences (user_id, event, in_app) VALUES ($1, 'message.received', false)`, member); err != nil {
		t.Fatalf("the sixth switch is refused: %v", err)
	}

	if err := goose.DownTo(conn, dir, sharing); err != nil {
		t.Fatalf("down: %v", err)
	}
	if n := present(); n != 0 {
		t.Fatalf("after down: %d messaging tables left", n)
	}
	var switches int
	if err := conn.QueryRow(`SELECT count(*) FROM app.notification_preferences WHERE user_id = $1`, member).Scan(&switches); err != nil || switches != 0 {
		t.Errorf("after down the student keeps %d switches (%v), want the message.received row gone", switches, err)
	}
	if _, err := conn.Exec(`INSERT INTO app.notification_preferences (user_id, event, in_app) VALUES ($1, 'message.received', false)`, member); !db.IsCheckViolation(err, "notification_preferences_event_check") {
		t.Errorf("after down message.received is %v, want R4's check to refuse it", err)
	}
	if err := goose.UpTo(conn, dir, messaging); err != nil {
		t.Fatalf("second up: %v", err)
	}
	if n := present(); n != 5 {
		t.Errorf("after the second up: %d tables, want 5", n)
	}
}

// TestTheTieFileDeletesTheAnonymisedAccountsTiesAndRollsBack runs 00169 over
// a teacher tied to an anonymised student, as 00167's fill ties one, and to
// one who is not. The anonymised student's tie goes and the other stays. Down
// gives the triggers 00167's bodies back, which tie an anonymised member
// again, and up runs a second time.
func TestTheTieFileDeletesTheAnonymisedAccountsTiesAndRollsBack(t *testing.T) {
	if os.Getenv("TEST_DESTRUCTIVE") != "1" {
		t.Skip("TEST_DESTRUCTIVE=1 required for an isolated migration database")
	}
	const before, tieFile int64 = 168, 169
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
	if err := goose.UpTo(conn, dir, before); err != nil {
		t.Fatal(err)
	}
	var teacher, kept, gone, class string
	withTx(t, conn, func(tx *sql.Tx, f fixture) {
		teacher, kept, class = f.adminID, f.studentID, f.classID
		if err := tx.QueryRow(`INSERT INTO app.users (email, full_name, role_id)
			VALUES ('gone-' || uuidv7() || '@example.com', 'Học viên', (SELECT id FROM app.roles WHERE builtin_key = 'student')) RETURNING id`).Scan(&gone); err != nil {
			t.Fatal(err)
		}
		mustExec(t, tx, `UPDATE app.users SET email = 'anonymous-' || id::text || '@anonymous.invalid', disabled_at = now() WHERE id = $1`, gone)
		mustExec(t, tx, `INSERT INTO app.messaging_ties (staff_id, student_id) VALUES ($1, $2), ($1, $3)`, teacher, kept, gone)
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	})
	tiedTo := func() map[string]bool {
		t.Helper()
		rows, err := conn.Query(`SELECT student_id::text FROM app.messaging_ties WHERE staff_id = $1`, teacher)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = rows.Close() }()
		out := map[string]bool{}
		for rows.Next() {
			var student string
			if err := rows.Scan(&student); err != nil {
				t.Fatal(err)
			}
			out[student] = true
		}
		return out
	}
	if err := goose.UpTo(conn, dir, tieFile); err != nil {
		t.Fatalf("up: %v", err)
	}
	if got := tiedTo(); !got[kept] || got[gone] || len(got) != 1 {
		t.Errorf("after up the teacher is tied to %v, want %s alone", got, kept)
	}

	if err := goose.DownTo(conn, dir, before); err != nil {
		t.Fatalf("down: %v", err)
	}
	tx, err := conn.Begin()
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, tx, `INSERT INTO app.class_members (class_id, user_id, joined_via, added_by) VALUES ($1, $2, 'admin', $3)`, class, gone, teacher)
	mustExec(t, tx, `SET CONSTRAINTS app.class_members_fill_messaging_ties IMMEDIATE`)
	var back bool
	if err := tx.QueryRow(`SELECT EXISTS (SELECT 1 FROM app.messaging_ties WHERE staff_id = $1 AND student_id = $2)`, teacher, gone).Scan(&back); err != nil {
		t.Fatal(err)
	}
	_ = tx.Rollback()
	if !back {
		t.Error("after down the member trigger still leaves an anonymised member out, want 00167's body back")
	}
	if err := goose.UpTo(conn, dir, tieFile); err != nil {
		t.Fatalf("second up: %v", err)
	}
}

// TestTheWidenedSwitchesTakeTheWeeklySummaryAndRollBack runs 00170, T-R7.13's
// widen_notification_preference_events, over saved switches. Up admits
// summary.weekly beside 00162's message.received, which is the plan's
// seven-key list. Its Down deletes the summary.weekly rows and keeps the
// message.received ones under 00162's six keys; 00162's own Down then deletes
// those and restores R4's five, so the two Downs together are the plan's one.
func TestTheWidenedSwitchesTakeTheWeeklySummaryAndRollBack(t *testing.T) {
	if os.Getenv("TEST_DESTRUCTIVE") != "1" {
		t.Skip("TEST_DESTRUCTIVE=1 required for an isolated migration database")
	}
	const before, widened, beforeSixth int64 = 169, 170, 161
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
	if err := goose.UpTo(conn, dir, before); err != nil {
		t.Fatal(err)
	}
	var teacher, student string
	withTx(t, conn, func(tx *sql.Tx, f fixture) {
		teacher, student = f.adminID, f.studentID
		mustExec(t, tx, `INSERT INTO app.notification_preferences (user_id, event, in_app, email) VALUES
			($1, 'attempt.flagged', true, false), ($2, 'message.received', false, false)`, teacher, student)
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	})
	weekly := func() error {
		_, err := conn.Exec(`INSERT INTO app.notification_preferences (user_id, event, in_app, email) VALUES ($1, 'summary.weekly', false, true)`, teacher)
		return err
	}
	if err := weekly(); !db.IsCheckViolation(err, "notification_preferences_event_check") {
		t.Fatalf("before 00170 summary.weekly is %v, want the check to refuse it", err)
	}
	if err := goose.UpTo(conn, dir, widened); err != nil {
		t.Fatalf("up over saved switches: %v", err)
	}
	if err := weekly(); err != nil {
		t.Fatalf("after 00170 summary.weekly is refused: %v", err)
	}
	var definition string
	if err := conn.QueryRow(`SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname = 'notification_preferences_event_check'`).Scan(&definition); err != nil {
		t.Fatal(err)
	}
	for _, event := range []string{"attempt.submitted", "attempt.flagged", "assignment.closing", "assignment.due_soon", "result.ready", "message.received", "summary.weekly"} {
		if !strings.Contains(definition, "'"+event+"'") {
			t.Errorf("the widened check %s lacks %s", definition, event)
		}
	}
	if strings.Contains(definition, "NOT VALID") {
		t.Errorf("the widened check was left unvalidated: %s", definition)
	}
	events := func() []string {
		t.Helper()
		rows, err := conn.Query(`SELECT event FROM app.notification_preferences WHERE user_id IN ($1, $2) ORDER BY event`, teacher, student)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = rows.Close() }()
		var out []string
		for rows.Next() {
			var e string
			if err := rows.Scan(&e); err != nil {
				t.Fatal(err)
			}
			out = append(out, e)
		}
		return out
	}
	if err := goose.DownTo(conn, dir, before); err != nil {
		t.Fatalf("down: %v", err)
	}
	if got := events(); !slices.Equal(got, []string{"attempt.flagged", "message.received"}) {
		t.Errorf("after 00170's down the switches are %v, want the weekly summary's row gone and the others kept", got)
	}
	if err := weekly(); !db.IsCheckViolation(err, "notification_preferences_event_check") {
		t.Errorf("after 00170's down summary.weekly is %v, want 00162's check to refuse it", err)
	}
	if err := goose.DownTo(conn, dir, beforeSixth); err != nil {
		t.Fatalf("down past 00162: %v", err)
	}
	if got := events(); !slices.Equal(got, []string{"attempt.flagged"}) {
		t.Errorf("after both downs the switches are %v, want R4's alone", got)
	}
	if err := goose.UpTo(conn, dir, widened); err != nil {
		t.Fatalf("second up: %v", err)
	}
	if err := weekly(); err != nil {
		t.Errorf("after the second up summary.weekly is refused: %v", err)
	}
}

// TestThePreR7SwitchesGetTheDecksEmailAndUpRunsAgain runs 00171 over the rows
// R4's settings page saved, each with email false. A staff member's switches
// whose email the deck draws on are turned on, those it draws off stay off,
// and a row set to true through the API is kept. A student's email becomes
// their in-app switch. Down leaves the rows, and up runs a second time.
func TestThePreR7SwitchesGetTheDecksEmailAndUpRunsAgain(t *testing.T) {
	if os.Getenv("TEST_DESTRUCTIVE") != "1" {
		t.Skip("TEST_DESTRUCTIVE=1 required for an isolated migration database")
	}
	const before, reset int64 = 170, 171
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
	if err := goose.UpTo(conn, dir, before); err != nil {
		t.Fatal(err)
	}
	var teacher, student string
	withTx(t, conn, func(tx *sql.Tx, f fixture) {
		teacher, student = f.adminID, f.studentID
		mustExec(t, tx, `INSERT INTO app.notification_preferences (user_id, event, in_app, email) VALUES
			($1, 'attempt.flagged', true, false), ($1, 'message.received', false, false), ($1, 'attempt.submitted', true, false),
			($1, 'assignment.closing', true, true), ($2, 'result.ready', true, false), ($2, 'assignment.due_soon', false, true)`, teacher, student)
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	})
	emails := func() map[string]bool {
		t.Helper()
		rows, err := conn.Query(`SELECT CASE WHEN user_id = $1 THEN 'staff ' ELSE 'student ' END || event, email
			FROM app.notification_preferences WHERE user_id IN ($1, $2)`, teacher, student)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = rows.Close() }()
		out := map[string]bool{}
		for rows.Next() {
			var key string
			var on bool
			if err := rows.Scan(&key, &on); err != nil {
				t.Fatal(err)
			}
			out[key] = on
		}
		return out
	}
	want := map[string]bool{
		"staff attempt.flagged": true, "staff message.received": true, "staff attempt.submitted": false,
		"staff assignment.closing": true, "student result.ready": true, "student assignment.due_soon": false,
	}
	if err := goose.UpTo(conn, dir, reset); err != nil {
		t.Fatalf("up: %v", err)
	}
	if got := emails(); !maps.Equal(got, want) {
		t.Errorf("after 00171 the email switches are %v, want %v", got, want)
	}
	if err := goose.DownTo(conn, dir, before); err != nil {
		t.Fatalf("down: %v", err)
	}
	if got := emails(); !maps.Equal(got, want) {
		t.Errorf("after 00171's down the email switches are %v, want them left as up wrote them", got)
	}
	if err := goose.UpTo(conn, dir, reset); err != nil {
		t.Fatalf("second up: %v", err)
	}
}

// TestTheSuppressionListComesAndGoesWithItsMigration runs 00172 up, down and
// up again on a database whose outbox already holds mail: Down drops the
// table, rows included, and leaves the outbox alone.
func TestTheSuppressionListComesAndGoesWithItsMigration(t *testing.T) {
	if os.Getenv("TEST_DESTRUCTIVE") != "1" {
		t.Skip("TEST_DESTRUCTIVE=1 required for an isolated migration database")
	}
	const before, suppressions int64 = 171, 172
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
	if err := goose.UpTo(conn, dir, before); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`INSERT INTO app.email_outbox (to_address, template, locale) VALUES ('waiting@example.com', 'account_invite', 'vi')`); err != nil {
		t.Fatal(err)
	}
	present := func() bool {
		t.Helper()
		var table *string
		if err := conn.QueryRow(`SELECT to_regclass('app.email_suppressions')::text`).Scan(&table); err != nil {
			t.Fatal(err)
		}
		return table != nil
	}
	if present() {
		t.Fatal("the list exists before 00172")
	}
	if err := goose.UpTo(conn, dir, suppressions); err != nil {
		t.Fatalf("up: %v", err)
	}
	if _, err := conn.Exec(`INSERT INTO app.email_suppressions (address, reason, provider_event_id) VALUES ('waiting@example.com', 'bounced', 'msg_1')`); err != nil {
		t.Fatalf("after up a suppression is refused: %v", err)
	}
	if err := goose.DownTo(conn, dir, before); err != nil {
		t.Fatalf("down: %v", err)
	}
	var waiting int
	if err := conn.QueryRow(`SELECT count(*) FROM app.email_outbox WHERE to_address = 'waiting@example.com'`).Scan(&waiting); err != nil || waiting != 1 || present() {
		t.Errorf("after down: list present %v, outbox rows %d %v; want the list gone and the outbox kept", present(), waiting, err)
	}
	if err := goose.UpTo(conn, dir, suppressions); err != nil {
		t.Fatalf("second up: %v", err)
	}
	var listed int
	if err := conn.QueryRow(`SELECT count(*) FROM app.email_suppressions`).Scan(&listed); err != nil || listed != 0 {
		t.Errorf("after the second up the list holds %d rows (%v), want an empty table", listed, err)
	}
}
