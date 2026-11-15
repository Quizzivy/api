//go:build integration

package db_test

import (
	"database/sql"
	"strings"
	"testing"
	"time"
)

type leadRow struct {
	name      string
	phone     string
	interests string
	courseFor string
	callTime  string
	locale    string
	version   string
	status    string
	note      *string
}

func validLead() leadRow {
	return leadRow{
		name: "Trần Thị Mai", phone: "0903123456", interests: "{ielts,kids}", courseFor: "child",
		callTime: "evening", locale: "vi", version: "2026-11", status: "new",
	}
}

const insertLead = `
	INSERT INTO app.leads
	       (full_name, phone_digits, interests, course_for, call_time, locale, privacy_notice_version, consented_at, status, note)
	VALUES ($1, $2, $3::text[], $4, $5, $6, $7, now(), $8, $9)`

func (r leadRow) args() []any {
	return []any{r.name, r.phone, r.interests, r.courseFor, r.callTime, r.locale, r.version, r.status, r.note}
}

// TestALeadHoldsOnlyWhatTheFormMayHold fails when 00156 loses one of its
// CHECK constraints or loosens its bound: each case breaks one rule of an
// otherwise valid row and names the constraint that must refuse it. No other
// test inserts into app.leads past the application's own normalising.
func TestALeadHoldsOnlyWhatTheFormMayHold(t *testing.T) {
	conn := migrated(t)
	withTx(t, conn, func(tx *sql.Tx, _ fixture) {
		note := strings.Repeat("ă", 1000)
		row := validLead()
		row.name, row.phone, row.note = strings.Repeat("ê", 120), "123456789012345", &note
		if _, err := tx.Exec(insertLead, row.args()...); err != nil {
			t.Fatalf("a row at every bound was refused: %v", err)
		}
	})
	empty, long := "", strings.Repeat("ă", 1001)
	for name, c := range map[string]struct {
		constraint string
		change     func(*leadRow)
	}{
		"an empty name":                  {"leads_full_name_check", func(r *leadRow) { r.name = "" }},
		"a name of 121 characters":       {"leads_full_name_check", func(r *leadRow) { r.name = strings.Repeat("ê", 121) }},
		"a name that ends in a space":    {"leads_full_name_check", func(r *leadRow) { r.name = "Mai " }},
		"a number of 8 digits":           {"leads_phone_digits_check", func(r *leadRow) { r.phone = "12345678" }},
		"a number of 16 digits":          {"leads_phone_digits_check", func(r *leadRow) { r.phone = "1234567890123456" }},
		"a number with a plus sign":      {"leads_phone_digits_check", func(r *leadRow) { r.phone = "+84903123456" }},
		"a course the form never offers": {"leads_interests_check", func(r *leadRow) { r.interests = "{ielts,chess}" }},
		"six interests":                  {"leads_interests_check", func(r *leadRow) { r.interests = "{ielts,toeic,communication,kids,business,ielts}" }},
		"a missing interest":             {"leads_interests_check", func(r *leadRow) { r.interests = "{ielts,NULL}" }},
		"a course for nobody listed":     {"leads_course_for_check", func(r *leadRow) { r.courseFor = "friend" }},
		"a call time nobody listed":      {"leads_call_time_check", func(r *leadRow) { r.callTime = "night" }},
		"a third language":               {"leads_locale_check", func(r *leadRow) { r.locale = "fr" }},
		"no notice version":              {"leads_privacy_notice_version_check", func(r *leadRow) { r.version = "" }},
		"a fourth status":                {"leads_status_check", func(r *leadRow) { r.status = "archived" }},
		"an empty note":                  {"leads_note_check", func(r *leadRow) { r.note = &empty }},
		"a note of 1001 characters":      {"leads_note_check", func(r *leadRow) { r.note = &long }},
	} {
		t.Run(name, func(t *testing.T) {
			withTx(t, conn, func(tx *sql.Tx, _ fixture) {
				row := validLead()
				c.change(&row)
				rejectsWith(t, tx, c.constraint, insertLead, row.args()...)
			})
		})
	}
}

// TestALeadOutlivesTheAccountThatHandledItAndMovesItsUpdatedAt fails when
// handled_by stops being ON DELETE SET NULL (the account's deletion is then
// refused or takes the request with it) and when the updated_at trigger of
// 00156 is dropped. It is the only test of those two properties of app.leads.
func TestALeadOutlivesTheAccountThatHandledItAndMovesItsUpdatedAt(t *testing.T) {
	conn := migrated(t)
	withTx(t, conn, func(tx *sql.Tx, f fixture) {
		var id string
		created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
		if err := tx.QueryRow(`
			INSERT INTO app.leads
			       (full_name, phone_digits, course_for, call_time, locale, privacy_notice_version, consented_at, created_at, updated_at)
			VALUES ('Trần Thị Mai', '0903123456', 'self', 'any', 'vi', '2026-11', $1, $1, $1)
			RETURNING id::text`, created).Scan(&id); err != nil {
			t.Fatalf("lead: %v", err)
		}
		if _, err := tx.Exec(`UPDATE app.leads SET status = 'contacted', handled_by = $2, handled_at = $3 WHERE id = $1`,
			id, f.studentID, created.Add(time.Hour)); err != nil {
			t.Fatalf("handle: %v", err)
		}
		var updated time.Time
		if err := tx.QueryRow(`SELECT updated_at FROM app.leads WHERE id = $1`, id).Scan(&updated); err != nil {
			t.Fatal(err)
		}
		if !updated.After(created) {
			t.Errorf("updated_at stayed at %v after a change", updated)
		}
		if _, err := tx.Exec(`DELETE FROM app.users WHERE id = $1`, f.studentID); err != nil {
			t.Fatalf("deleting the account that handled the request: %v", err)
		}
		var handler *string
		var handledAt *time.Time
		if err := tx.QueryRow(`SELECT handled_by::text, handled_at FROM app.leads WHERE id = $1`, id).Scan(&handler, &handledAt); err != nil {
			t.Fatalf("the request went with the account: %v", err)
		}
		if handler != nil || handledAt == nil {
			t.Errorf("handled_by = %v, handled_at = %v; want no account and the time kept", handler, handledAt)
		}
	})
}

// TestTheAppRoleReadsWritesChangesAndRemovesLeads fails when a migration
// narrows what quizzivy_app holds on app.leads: the form inserts, the console
// reads and updates, and an Admin deletes on request.
// TestAppRoleCanReadAndWriteEveryTable checks SELECT and INSERT only.
func TestTheAppRoleReadsWritesChangesAndRemovesLeads(t *testing.T) {
	conn := migrated(t)
	for _, priv := range []string{"SELECT", "INSERT", "UPDATE", "DELETE"} {
		if !hasTablePrivilege(t, conn, appRole, "app.leads", priv) {
			t.Errorf("%s cannot %s app.leads", appRole, priv)
		}
	}
}

// TestTheSettingsRowHoldsAtMostFiveLeadRecipients fails when 00157's CHECK is
// dropped or its bound moves, and when the column loses its empty default.
func TestTheSettingsRowHoldsAtMostFiveLeadRecipients(t *testing.T) {
	conn := migrated(t)
	withTx(t, conn, func(tx *sql.Tx, _ fixture) {
		if _, err := tx.Exec(`SELECT 1 FROM app.organization_settings FOR UPDATE`); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(`UPDATE app.organization_settings SET lead_recipients = DEFAULT`); err != nil {
			t.Fatal(err)
		}
		var held int
		if err := tx.QueryRow(`SELECT cardinality(lead_recipients) FROM app.organization_settings`).Scan(&held); err != nil || held != 0 {
			t.Fatalf("the default holds %d recipients (%v), want none", held, err)
		}
		if _, err := tx.Exec(`UPDATE app.organization_settings SET lead_recipients = '{a@x.example,b@x.example,c@x.example,d@x.example,e@x.example}'`); err != nil {
			t.Fatalf("five recipients were refused: %v", err)
		}
		rejectsWith(t, tx, "organization_settings_lead_recipients_check",
			`UPDATE app.organization_settings SET lead_recipients = '{a@x.example,b@x.example,c@x.example,d@x.example,e@x.example,f@x.example}'`)
	})
}

// TestALeadRowHoldsWhatTheFormAskedForAndNothingAboutTheCaller fails when a
// migration gives app.leads a column the form never sent: an IP address, a
// user agent or any other trace of the device. The contract promises the
// stored request holds none, so a new column takes a decision, not a diff.
func TestALeadRowHoldsWhatTheFormAskedForAndNothingAboutTheCaller(t *testing.T) {
	conn := migrated(t)
	var columns string
	if err := conn.QueryRow(`
		SELECT string_agg(attname, ',' ORDER BY attnum)
		  FROM pg_attribute
		 WHERE attrelid = 'app.leads'::regclass AND attnum > 0 AND NOT attisdropped`).Scan(&columns); err != nil {
		t.Fatal(err)
	}
	want := "id,full_name,phone_digits,interests,course_for,call_time,locale,privacy_notice_version,consented_at," +
		"status,note,handled_by,handled_at,created_at,updated_at"
	if columns != want {
		t.Errorf("app.leads holds the columns %s, want %s", columns, want)
	}
}
