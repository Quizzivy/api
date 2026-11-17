//go:build integration

package maintenance_test

import (
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"quizzivy/internal/platform/email"
)

// decisionEmail is a credit decision's row in R5's outbox, as the console
// queued it.
type decisionEmail struct {
	address, locale, dedupe string
	params                  map[string]any
	sealed, due, waiting    bool
}

// decisionEmails reads the decision emails queued for teacher about request,
// through the owner's connection.
func (w *aiWorld) decisionEmails(teacher, request string) []decisionEmail {
	w.t.Helper()
	rows, err := w.tx.Query(w.ctx, `
		SELECT to_address, locale, dedupe_key, params, secret_ciphertext IS NOT NULL, deliver_after <= now(), sent_at IS NULL AND failed_at IS NULL
		  FROM app.email_outbox
		 WHERE to_user_id = $1::uuid AND template = $2 AND params->>'requestId' = $3`,
		teacher, string(email.AICreditRequestDecided), request)
	if err != nil {
		w.t.Fatal(err)
	}
	defer rows.Close()
	var out []decisionEmail
	for rows.Next() {
		var e decisionEmail
		var params []byte
		if err := rows.Scan(&e.address, &e.locale, &e.dedupe, &params, &e.sealed, &e.due, &e.waiting); err != nil {
			w.t.Fatal(err)
		}
		if err := json.Unmarshal(params, &e.params); err != nil {
			w.t.Fatal(err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		w.t.Fatal(err)
	}
	return out
}

// request files a pending request of teacher's for 7,000 credits in month.
func (w *aiWorld) request(teacher, month string) string {
	w.t.Helper()
	var id string
	if err := w.tx.QueryRow(w.ctx, `INSERT INTO app.ai_credit_requests (user_id, month, amount_credits, reason)
		VALUES ($1::uuid, $2::date, 7000, 'Cần thêm') RETURNING id::text`, teacher, month).Scan(&id); err != nil {
		w.t.Fatal(err)
	}
	return id
}

// teacherFor is a teacher whose credits row exists this month, writing in
// locale (empty for none, which the outbox reads as vi).
func (w *aiWorld) teacherFor(locale string) (id, address string) {
	w.t.Helper()
	id = w.user("teacher")
	if err := w.tx.QueryRow(w.ctx, `UPDATE app.users SET locale = nullif($2, '') WHERE id = $1::uuid RETURNING email`, id, locale).Scan(&address); err != nil {
		w.t.Fatal(err)
	}
	w.run("ai-credits", "set-allowance", "-user", id, "-credits", "5000", "-operator", "thuong", "-apply")
	return id, address
}

func (w *aiWorld) decisionNotices(teacher string) int {
	w.t.Helper()
	return w.count(`SELECT count(*) FROM app.notifications WHERE user_id = $1::uuid AND kind = 'ai.credit_request.decided'`, teacher)
}

// TestApproveAndDeclineEmailTheTeacherAsTheMaintenanceRole is the owner's
// decision of 2026-11-16 over T-R7.23: run as quizzivy_maintenance, a dry run
// of approve or decline queues no email, and each with -apply queues exactly
// one, the row the Admin operation writes: the decision template for the
// teacher who asked, at their own address and in their own language, holding
// the request's id alone, keyed by the request and the teacher, with no
// secret, due at once and waiting for the API's dispatcher.
func TestApproveAndDeclineEmailTheTeacherAsTheMaintenanceRole(t *testing.T) {
	iso := newIsolated(t)
	w := &aiWorld{t: t, ctx: iso.ctx, tx: iso.owner, conn: iso.role, month: aiMonth(t)}
	for _, c := range []struct {
		command, locale, want string
	}{
		{"approve", "en", "en"},
		{"decline", "", "vi"},
	} {
		teacher, address := w.teacherFor(c.locale)
		request := w.request(teacher, w.month)

		w.run("ai-credits", c.command, "-request", request, "-operator", "thuong")
		if got := w.decisionEmails(teacher, request); len(got) != 0 || w.decisionNotices(teacher) != 0 {
			t.Errorf("a dry run of %s queued %+v and told the teacher %d times", c.command, got, w.decisionNotices(teacher))
		}

		w.run("ai-credits", c.command, "-request", request, "-operator", "thuong", "-apply")
		got := w.decisionEmails(teacher, request)
		if len(got) != 1 {
			t.Fatalf("%s -apply queued %d decision emails for the teacher, want 1", c.command, len(got))
		}
		e := got[0]
		if e.address != address || e.locale != c.want || e.dedupe != "email:ai_decision:"+request+":"+teacher {
			t.Errorf("%s queued to %q in %q keyed %q, want %q in %q keyed by the request and the teacher", c.command, e.address, e.locale, e.dedupe, address, c.want)
		}
		if !maps.Equal(e.params, map[string]any{"requestId": request}) || e.sealed || !e.due || !e.waiting {
			t.Errorf("%s's row holds %v (sealed %v, due %v, waiting %v), want the request's id alone, unsealed, due and waiting", c.command, e.params, e.sealed, e.due, e.waiting)
		}
		if n := w.decisionNotices(teacher); n != 1 {
			t.Errorf("%s -apply told the teacher %d times in the app, want 1", c.command, n)
		}
		if n := w.count(`SELECT count(*) FROM app.email_outbox WHERE to_user_id = $1::uuid`, teacher); n != 1 {
			t.Errorf("the teacher has %d emails queued, want the decision alone", n)
		}
	}
}

// TestTheConsoleEmailsNoDecisionThatNotifyEmailEventsTurnsOff keeps the
// console to the API's switch: with ai.credit_request.decided left out of
// NOTIFY_EMAIL_EVENTS, or none, an applied decision tells the teacher in the
// app and queues no email, and a value the API would refuse fails the command
// before it decides anything.
func TestTheConsoleEmailsNoDecisionThatNotifyEmailEventsTurnsOff(t *testing.T) {
	iso := newIsolated(t)
	w := &aiWorld{t: t, ctx: iso.ctx, tx: iso.owner, conn: iso.role, month: aiMonth(t)}
	others := slices.DeleteFunc(email.NotifyEvents(), func(event string) bool { return event == "ai.credit_request.decided" })
	for _, events := range []string{strings.Join(others, ","), "none"} {
		t.Setenv("NOTIFY_EMAIL_EVENTS", events)
		teacher, _ := w.teacherFor("")
		request := w.request(teacher, w.month)
		w.run("ai-credits", "approve", "-request", request, "-operator", "thuong", "-apply")
		if got := w.decisionEmails(teacher, request); len(got) != 0 {
			t.Errorf("NOTIFY_EMAIL_EVENTS=%s still queued %+v", events, got)
		}
		if n := w.decisionNotices(teacher); n != 1 {
			t.Errorf("NOTIFY_EMAIL_EVENTS=%s told the teacher %d times in the app, want 1", events, n)
		}
	}

	teacher, _ := w.teacherFor("")
	request := w.request(teacher, w.month)
	t.Setenv("NOTIFY_EMAIL_EVENTS", "ai.credit_request.decide")
	if err := w.fails("ai-credits", "approve", "-request", request, "-operator", "thuong", "-apply"); !strings.Contains(err.Error(), "NOTIFY_EMAIL_EVENTS") {
		t.Errorf("a misspelt NOTIFY_EMAIL_EVENTS failed with %v, want the setting named", err)
	}
	if n := w.count(`SELECT count(*) FROM app.ai_credit_requests WHERE id = $1::uuid AND status = 'pending'`, request); n != 1 {
		t.Error("a misspelt NOTIFY_EMAIL_EVENTS still decided the request")
	}
}

// TestTheMaintenanceRoleQueuesNoOtherEmail is 00173's guard and its column
// grants at work: the role may queue a credit decision email to an account's
// own address and nothing else, may not read what a queued row says, and may
// neither change nor delete one.
func TestTheMaintenanceRoleQueuesNoOtherEmail(t *testing.T) {
	iso := newIsolated(t)
	w := &aiWorld{t: t, ctx: iso.ctx, tx: iso.owner, conn: iso.role, month: aiMonth(t)}
	teacher, address := w.teacherFor("")
	insert := `INSERT INTO app.email_outbox (to_user_id, to_address, template, locale, params, secret_ciphertext, dedupe_key)
		VALUES (nullif($1, '')::uuid, $2, $3, 'vi', '{"requestId": "00000000-0000-0000-0000-000000000001"}', $4, gen_random_uuid()::text)`
	refused := func(what string, err error) {
		t.Helper()
		var pg *pgconn.PgError
		if !errors.As(err, &pg) || pg.Code != "42501" {
			t.Errorf("%s: err = %v, want 42501", what, err)
		}
	}
	decided := string(email.AICreditRequestDecided)
	for _, c := range []struct {
		what, user, address, template string
		secret                        []byte
	}{
		{"another template", teacher, address, string(email.AccountInvite), nil},
		{"another address", teacher, "taken-over@example.test", decided, nil},
		{"no account", "", address, decided, nil},
		{"a sealed secret", teacher, address, decided, []byte{1, 2, 3}},
	} {
		_, err := iso.role.Exec(iso.ctx, insert, c.user, c.address, c.template, c.secret)
		refused("queuing "+c.what, err)
	}
	if _, err := iso.role.Exec(iso.ctx, insert, teacher, address, decided, nil); err != nil {
		t.Fatalf("queuing a decision email to the teacher's own address: %v", err)
	}
	for what, sql := range map[string]string{
		"reading a queued row's address": `SELECT to_address FROM app.email_outbox LIMIT 1`,
		"reading a queued row's params":  `SELECT params FROM app.email_outbox LIMIT 1`,
		"holding a queued row back":      `UPDATE app.email_outbox SET deliver_after = now() + interval '1 day'`,
		"deleting a queued row":          `DELETE FROM app.email_outbox`,
		"reading the suppression list":   `SELECT address FROM app.email_suppressions LIMIT 1`,
	} {
		_, err := iso.role.Exec(iso.ctx, sql)
		refused(what, err)
	}
	if n := w.count(`SELECT count(*) FROM app.email_outbox WHERE to_user_id = $1::uuid AND deliver_after <= now() + interval '1 minute'`, teacher); n != 1 {
		t.Errorf("the teacher has %d emails queued, want the one the role was let write", n)
	}
}
