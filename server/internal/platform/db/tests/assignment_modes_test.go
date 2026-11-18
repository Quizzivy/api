package db_test

import (
	"database/sql"
	"testing"
	"time"
)

// F1's migration (00174) during the rolling deploy: the v0.14.0 pod inserts
// an assignment and an attempt naming none of the new columns, and they read
// strict and unmeasured.
func TestARowInsertedTheV0140WayReadsStrictAndUnmeasured(t *testing.T) {
	withTx(t, migrated(t), func(tx *sql.Tx, f fixture) {
		section := newVersion(t, tx, f.adminID)
		var version, assignment, attempt string
		if err := tx.QueryRow(`SELECT test_version_id FROM app.test_version_sections WHERE id = $1`, section).Scan(&version); err != nil {
			t.Fatal(err)
		}
		now := time.Now()
		if err := tx.QueryRow(`
			INSERT INTO app.assignments
			       (test_id, test_version_id, opens_at, closes_at,
			        duration_minutes, max_attempts, shuffle_questions, shuffle_options,
			        review_show_score, review_show_correct_answers, review_show_explanations,
			        review_release, review_show_class_average, student_note,
			        integrity_require_fullscreen, integrity_block_copy_paste,
			        integrity_max_focus_loss, integrity_on_limit_exceeded, integrity_min_away_ms,
			        created_by, published_at)
			SELECT v.test_id, v.id, $2, $3, 45, 1, false, false, true, false, false,
			       'on_submit', false, NULL, false, true, 0, 'flag', 3000, $4, $2
			  FROM app.test_versions v WHERE v.id = $1
			RETURNING id`, version, now.Add(-time.Hour), now.Add(time.Hour), f.adminID).Scan(&assignment); err != nil {
			t.Fatalf("the v0.14.0 assignment insert: %v", err)
		}
		if err := tx.QueryRow(`
			INSERT INTO app.attempts
			  (assignment_id, test_version_id, student_id, attempt_no, session_id,
			   shuffle_seed, beacon_token_hash, started_at, deadline_at)
			VALUES ($1, $2, $3, 1, gen_random_uuid(), 1, sha256('b'::bytea), $4, $5)
			RETURNING id`, assignment, version, f.studentID, now, now.Add(45*time.Minute)).Scan(&attempt); err != nil {
			t.Fatalf("the v0.14.0 attempt insert: %v", err)
		}
		var mode string
		var activeMs, reportedAt sql.NullString
		if err := tx.QueryRow(`SELECT a.mode::text, at.active_ms::text, at.active_reported_at::text
			  FROM app.attempts at JOIN app.assignments a ON a.id = at.assignment_id WHERE at.id = $1`, attempt).
			Scan(&mode, &activeMs, &reportedAt); err != nil {
			t.Fatal(err)
		}
		if mode != "strict" || activeMs.Valid || reportedAt.Valid {
			t.Errorf("the v0.14.0 rows read mode %q, active_ms %v, active_reported_at %v; want strict and both NULL", mode, activeMs, reportedAt)
		}
		rejectsWith(t, tx, "attempts_active_ms_check", `UPDATE app.attempts SET active_ms = -1 WHERE id = $1`, attempt)
	})
}
