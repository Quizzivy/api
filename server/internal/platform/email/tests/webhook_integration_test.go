//go:build integration

package email_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"quizzivy/internal/platform/db"
	"quizzivy/internal/platform/email"
)

// lockedBuffer is a log sink the dispatcher and the test can share.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func newWebhook(t *testing.T, pool *pgxpool.Pool, logs *lockedBuffer) *email.Webhook {
	t.Helper()
	webhook, err := email.NewWebhook(webhookSecret, db.NewContext(pool), slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	if err != nil {
		t.Fatal(err)
	}
	webhook.Now = func() time.Time { return webhookNow }
	return webhook
}

type suppression struct{ reason, eventID string }

func suppressionsOf(t *testing.T, pool *pgxpool.Pool, addresses ...string) map[string]suppression {
	t.Helper()
	rows, err := pool.Query(context.Background(), `SELECT address, reason, provider_event_id FROM app.email_suppressions WHERE address = ANY($1::text[])`, addresses)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]suppression{}
	for rows.Next() {
		var address string
		var s suppression
		if err := rows.Scan(&address, &s.reason, &s.eventID); err != nil {
			t.Fatal(err)
		}
		out[address] = s
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func forget(t *testing.T, pool *pgxpool.Pool, addresses ...string) {
	t.Helper()
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM app.email_suppressions WHERE address = ANY($1::text[])`, addresses); err != nil {
			t.Errorf("removing the suppressions: %v", err)
		}
	})
}

func eventFor(kind, to string) string {
	return `{"type":"` + kind + `","created_at":"2031-03-10T05:00:00.000Z","data":{"email_id":"4ef9a417","from":"Quizzivy <no-reply@quizzivy.com>","to":["` + to + `"],"subject":"Đề giữa kỳ"}}`
}

// TestABounceOrAComplaintListsItsRecipientAndAReplayChangesNothing fails when
// a signed hard bounce or complaint does not list its recipient as the
// dispatcher compares it, when a replayed svix-id or a second event for a
// listed address changes the list, when another event type lists anything,
// or when the log carries the address.
func TestABounceOrAComplaintListsItsRecipientAndAReplayChangesNothing(t *testing.T) {
	pool := newPool(t)
	logs := &lockedBuffer{}
	webhook := newWebhook(t, pool, logs)
	n := nonce(t)
	bounced, complained, delivered, replayed := "bounced-"+n+"@example.com", "complained-"+n+"@example.com", "delivered-"+n+"@example.com", "replayed-"+n+"@example.com"
	forget(t, pool, bounced, complained, delivered, replayed)
	receive := func(id, kind, to string) {
		t.Helper()
		if err := webhook.Receive(context.Background(), signed(signingKey, id, webhookNow, eventFor(kind, to))); err != nil {
			t.Fatalf("%s %s: %v", id, kind, err)
		}
	}

	receive("msg_b"+n, email.EventBounced, "  Bounced-"+n+"@Example.COM")
	receive("msg_c"+n, email.EventComplained, complained)
	receive("msg_d"+n, "email.delivered", delivered)
	receive("msg_o"+n, "email.opened", delivered)
	want := map[string]suppression{bounced: {"bounced", "msg_b" + n}, complained: {"complained", "msg_c" + n}}
	if got := suppressionsOf(t, pool, bounced, complained, delivered); len(got) != 2 || got[bounced] != want[bounced] || got[complained] != want[complained] {
		t.Fatalf("listed %+v, want %+v", got, want)
	}

	receive("msg_b"+n, email.EventBounced, bounced)
	receive("msg_b"+n, email.EventBounced, replayed)
	receive("msg_c2"+n, email.EventComplained, bounced)
	if got := suppressionsOf(t, pool, bounced, complained, replayed); len(got) != 2 || got[bounced] != want[bounced] || got[complained] != want[complained] {
		t.Errorf("a replayed svix-id or a second event changed the list: %+v", got)
	}
	if strings.Contains(strings.ToLower(logs.String()), n+"@") || !strings.Contains(logs.String(), "toDomain=example.com") {
		t.Errorf("the log names the address, or not the domain: %s", logs.String())
	}
}

// TestASuppressedAddressIsClosedAsSuppressedAndNeverSent fails when the
// dispatcher sends to a listed address in whatever case the outbox holds it,
// retries it, asks its Preparer, spends an attempt or the day's budget on it,
// keeps its secret, or logs more of it than its domain; and when it stops
// sending to the address beside it.
func TestASuppressedAddressIsClosedAsSuppressedAndNeverSent(t *testing.T) {
	w := newWorld(t, day(24))
	logs := &lockedBuffer{}
	w.disp.Logger = slog.New(slog.NewTextHandler(logs, nil))
	w.disp.DailyLimit = 1
	p := &preparing{send: true}
	w.disp.Prepare = email.Preparers{email.AttemptFlagged: p}
	n := nonce(t)
	listed, other := "listed-"+n+"@example.com", "other-"+n+"@example.com"
	forget(t, w.pool, listed)
	if _, err := email.Suppress(context.Background(), w.pool, email.Suppression{Address: listed, Reason: email.ReasonBounced, EventID: "msg_" + n}); err != nil {
		t.Fatal(err)
	}
	user := newUser(t, w.pool, "vi")
	invited := w.enqueue(t, invite("Listed-"+n+"@EXAMPLE.com"))
	notice := w.enqueue(t, flagged(listed, user, someAttempt))
	sent := w.enqueue(t, invite(other))

	batch, err := w.disp.Dispatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{invited, notice} {
		r := read(t, w.pool, id)
		if r.sentAt != nil || r.failedAt == nil || r.lastError != email.SuppressedCode || r.attempts != 0 || r.hasSecret {
			t.Errorf("the row to the listed address is %+v, want failed with SUPPRESSED, no attempt and no secret", r)
		}
	}
	if r := read(t, w.pool, sent); r.sentAt == nil {
		t.Errorf("the row beside it was not sent: %+v (%+v)", r, batch)
	}
	if batch.Suppressed != 2 || batch.Sent != 1 || batch.Skipped != 0 {
		t.Errorf("batch %+v, want two suppressed and one sent on a daily budget of one", batch)
	}
	for _, m := range w.sender.sent {
		if strings.EqualFold(m.To, listed) {
			t.Errorf("a message went to the listed address: %+v", m)
		}
	}
	for _, d := range p.drafts {
		if d.ToUserID == user {
			t.Errorf("the Preparer was asked about a row to a listed address: %+v", d)
		}
	}
	late := w.enqueue(t, invite(listed))
	again, err := w.disp.Dispatch(context.Background())
	if err != nil || again.Suppressed != 1 || again.Deferred != 0 {
		t.Errorf("with the day's budget spent, a dispatch did %+v %v, want the one new row closed and nothing deferred", again, err)
	}
	if r := read(t, w.pool, late); r.lastError != email.SuppressedCode || r.failedAt == nil {
		t.Errorf("the row written once the budget was spent is %+v, want it closed as SUPPRESSED, not held to midnight", r)
	}
	if out := logs.String(); strings.Contains(strings.ToLower(out), "listed-"+n) || strings.Count(out, "outcome=suppressed") != 3 || !strings.Contains(out, "toDomain=example.com") {
		t.Errorf("the log names the address, or not each suppression by its domain: %s", out)
	}
}

// TestSuppressRefusesWhatIsNoAddressAndKeepsTheFirstReason fails when Suppress
// stores something that is not one address, takes an unknown reason or no
// event id, or lets a later reason replace the first.
func TestSuppressRefusesWhatIsNoAddressAndKeepsTheFirstReason(t *testing.T) {
	pool := newPool(t)
	n := nonce(t)
	address := "first-" + n + "@example.com"
	forget(t, pool, address)
	for name, s := range map[string]email.Suppression{
		"no address":     {Address: "nobody", Reason: email.ReasonBounced, EventID: "msg_x" + n},
		"another reason": {Address: address, Reason: "unsubscribed", EventID: "msg_y" + n},
		"no event id":    {Address: address, Reason: email.ReasonBounced},
	} {
		if _, err := email.Suppress(context.Background(), pool, s); err == nil {
			t.Errorf("%s was stored", name)
		}
	}
	if listed, err := email.Suppress(context.Background(), pool, email.Suppression{Address: address, Reason: email.ReasonComplained, EventID: "msg_1" + n}); err != nil || !listed {
		t.Fatalf("the first suppression: %v %v", listed, err)
	}
	if listed, err := email.Suppress(context.Background(), pool, email.Suppression{Address: strings.ToUpper(address), Reason: email.ReasonBounced, EventID: "msg_2" + n}); err != nil || listed {
		t.Errorf("a second suppression of the address: %v %v, want nothing written", listed, err)
	}
	if got := suppressionsOf(t, pool, address); got[address] != (suppression{"complained", "msg_1" + n}) {
		t.Errorf("the list holds %+v", got)
	}
	if _, err := email.Suppress(context.Background(), pool, email.Suppression{Address: "x@", Reason: email.ReasonBounced, EventID: "msg_3" + n}); !errors.Is(err, email.ErrNotAnAddress) {
		t.Errorf("an address with no domain: %v", err)
	}
}
