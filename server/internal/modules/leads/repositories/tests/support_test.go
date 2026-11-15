//go:build integration

package repositories_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"quizzivy/internal/modules/leads/application"
	"quizzivy/internal/modules/leads/application/command"
	"quizzivy/internal/modules/leads/domain"
	"quizzivy/internal/modules/leads/repositories"
	"quizzivy/internal/platform/db"
	"quizzivy/internal/platform/email"
)

const (
	typedName = "Trần Thị Mai"
	leadNote  = "Gọi lại sau giờ tan học, hỏi lớp IELTS buổi tối"
)

func connect(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func nonce(t *testing.T) string {
	t.Helper()
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b)
}

func randomPhone(t *testing.T) string {
	t.Helper()
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000_000))
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("09%09d", n.Int64())
}

type world struct {
	t     *testing.T
	ctx   context.Context
	tx    pgx.Tx
	repo  *repositories.Postgres
	app   *application.Application
	now   time.Time
	nonce string
	actor string
}

func newWorld(t *testing.T) *world {
	t.Helper()
	pool := connect(t)
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	t.Cleanup(func() {
		if err := tx.Rollback(ctx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
			t.Errorf("rolling back the test's transaction: %v", err)
		}
	})
	w := &world{t: t, ctx: ctx, tx: tx, nonce: nonce(t), now: time.Date(2026, 11, 15, 2, 30, 0, 0, time.UTC)}
	w.exec(`SET LOCAL lock_timeout = '30s'`)
	w.exec(`SELECT 1 FROM app.organization_settings FOR UPDATE`)
	w.recipients()
	w.repo = repositories.NewPostgres(db.NewContext(tx), email.NewOutbox(nil))
	w.app = application.New(w.repo, slog.New(slog.DiscardHandler))
	w.app.SetClock(func() time.Time { return w.now })
	w.actor = w.user("quan-tri", "Cô Thương", "vi")
	return w
}

func (w *world) exec(sql string, args ...any) {
	w.t.Helper()
	if _, err := w.tx.Exec(w.ctx, sql, args...); err != nil {
		w.t.Fatalf("%s: %v", sql, err)
	}
}

func (w *world) count(sql string, args ...any) int {
	w.t.Helper()
	var n int
	if err := w.tx.QueryRow(w.ctx, sql, args...).Scan(&n); err != nil {
		w.t.Fatalf("%s: %v", sql, err)
	}
	return n
}

func (w *world) recipients(addresses ...string) {
	w.t.Helper()
	if addresses == nil {
		addresses = []string{}
	}
	w.exec(`UPDATE app.organization_settings SET lead_recipients = $1`, addresses)
}

func (w *world) address(local string) string {
	return local + "-" + w.nonce + "@example.com"
}

func (w *world) user(local, name, locale string) string {
	w.t.Helper()
	var id string
	if err := w.tx.QueryRow(w.ctx, `
		INSERT INTO app.users (email, full_name, role_id, locale)
		VALUES ($1, $2, (SELECT id FROM app.roles WHERE builtin_key = 'admin'), $3)
		RETURNING id::text`, w.address(local), name, locale).Scan(&id); err != nil {
		w.t.Fatalf("user: %v", err)
	}
	return id
}

func (w *world) form(phone string) domain.Form {
	return domain.Form{
		FullName: typedName, Phone: phone, Interests: []string{"ielts", "kids"}, CourseFor: "child",
		CallTime: "evening", Locale: "vi", PrivacyNoticeVersion: "2026-11",
	}
}

func (w *world) submit(f domain.Form) {
	w.t.Helper()
	if _, err := w.app.Commands.Submit.Handle(w.ctx, command.Submit{Form: f}); err != nil {
		w.t.Fatalf("submit: %v", err)
	}
}

func (w *world) stored(digits string) []string {
	w.t.Helper()
	rows, err := w.tx.Query(w.ctx, `SELECT id::text FROM app.leads WHERE phone_digits = $1 ORDER BY created_at, id`, digits)
	if err != nil {
		w.t.Fatal(err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		w.t.Fatal(err)
	}
	return ids
}

func (w *world) lead(name, digits, status string, created time.Time) string {
	w.t.Helper()
	var id string
	if err := w.tx.QueryRow(w.ctx, `
		INSERT INTO app.leads
		       (full_name, phone_digits, interests, course_for, call_time, locale, privacy_notice_version,
		        consented_at, created_at, status)
		VALUES ($1, $2, '{toeic}', 'self', 'morning', 'en', '2026-11', $3, $3, $4)
		RETURNING id::text`, name, digits, created, status).Scan(&id); err != nil {
		w.t.Fatalf("lead: %v", err)
	}
	return id
}

type outboxRow struct {
	to       string
	userID   *string
	template string
	locale   string
	params   string
	whole    string
}

func (w *world) outbox(leadID string) []outboxRow {
	w.t.Helper()
	rows, err := w.tx.Query(w.ctx, `
		SELECT o.to_address, o.to_user_id::text, o.template, o.locale, o.params::text, row_to_json(o)::text
		  FROM app.email_outbox o
		 WHERE o.params->>'leadId' = $1
		 ORDER BY o.to_address`, leadID)
	if err != nil {
		w.t.Fatal(err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (outboxRow, error) {
		var r outboxRow
		err := row.Scan(&r.to, &r.userID, &r.template, &r.locale, &r.params, &r.whole)
		return r, err
	})
	if err != nil {
		w.t.Fatal(err)
	}
	return out
}

type auditRow struct {
	action   string
	entity   string
	actor    *string
	ip       *string
	agent    *string
	occurred time.Time
	diff     *string
	whole    string
}

func (w *world) audits(leadID string) []auditRow {
	w.t.Helper()
	rows, err := w.tx.Query(w.ctx, `
		SELECT a.action, a.entity, a.actor_user_id::text, host(a.ip), a.user_agent, a.occurred_at, a.diff::text, row_to_json(a)::text
		  FROM app.audit_log a
		 WHERE a.entity_id = $1::uuid
		 ORDER BY a.id`, leadID)
	if err != nil {
		w.t.Fatal(err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (auditRow, error) {
		var r auditRow
		err := row.Scan(&r.action, &r.entity, &r.actor, &r.ip, &r.agent, &r.occurred, &r.diff, &r.whole)
		return r, err
	})
	if err != nil {
		w.t.Fatal(err)
	}
	return out
}

func (w *world) update(id string, change domain.Change) domain.Lead {
	w.t.Helper()
	lead, err := w.repo.Update(w.ctx, domain.Update{
		ID: id, Change: change, ActorID: w.actor, IP: "203.0.113.9", UserAgent: "leads-test/" + w.nonce, Now: w.now,
	})
	if err != nil {
		w.t.Fatalf("update %+v: %v", change, err)
	}
	return lead
}

func status(s domain.Status) *domain.Status { return &s }
