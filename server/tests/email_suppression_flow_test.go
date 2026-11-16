//go:build e2e

package e2e

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"quizzivy/internal/platform/config"
	"quizzivy/internal/platform/email"
)

var suppressionKey = []byte("e2e-webhook-signing-key!")

// deliverEvent posts a Resend event about to through the application's
// webhook, signed with key, and answers the status and the body.
func (w *world) deliverEvent(t *testing.T, key []byte, id, kind, to string) (int, string) {
	t.Helper()
	body := `{"type":"` + kind + `","created_at":"2031-03-10T05:00:00.000Z","data":{"email_id":"4ef9a417","from":"Quizzivy <no-reply@quizzivy.com>","to":["` + to + `"],"subject":"x"}}`
	req, err := http.NewRequest(http.MethodPost, w.server.URL+"/public/email-events", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(id + "." + ts + "." + body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("svix-id", id)
	req.Header.Set("svix-timestamp", ts)
	req.Header.Set("svix-signature", "v1,"+base64.StdEncoding.EncodeToString(mac.Sum(nil)))
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	answer, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(answer)
}

// TestABouncedAddressGetsNoMoreMailAndAForgedOneChangesNothing is T-R7.15
// through HTTP and R5's dispatcher: two invites are queued, then a signed
// hard bounce lists one address, after which its set-password invite is
// closed as SUPPRESSED and never sent, while a forged bounce for the other
// address is refused 401 and that address's invite goes. A signed bounce for
// an address Quizzivy does not hold is answered the same 204 and lists
// nothing. Every answer is empty.
func TestABouncedAddressGetsNoMoreMailAndAForgedOneChangesNothing(t *testing.T) {
	w := boot(t, func(c *config.Config) {
		c.SecretsKey = []byte(importSecretsKey)
		c.Email.WebhookSecret = config.Secret("whsec_" + base64.StdEncoding.EncodeToString(suppressionKey))
	})
	adminEmail, adminPassword := w.teacher()
	admin := w.signedIn(adminEmail, adminPassword)
	var roleID string
	if err := w.pool.QueryRow(context.Background(), `SELECT id::text FROM app.roles WHERE builtin_key = 'student'`).Scan(&roleID); err != nil {
		t.Fatal(err)
	}
	n := nonce(t)
	bounced, forged := "hoc-vien-"+n+"@example.com", "ban-"+n+"@example.com"
	t.Cleanup(func() {
		_, _ = w.pool.Exec(context.Background(), `DELETE FROM app.email_suppressions WHERE address = ANY($1::text[])`, []string{bounced, forged, "nobody-" + n + "@example.com"})
	})

	for _, address := range []string{bounced, forged} {
		admin.must(http.StatusCreated, http.MethodPost, "/admin/users", map[string]any{
			"email": address, "fullName": "Học viên", "roleId": roleID, "delivery": "email_link",
		})
	}
	if status, body := w.deliverEvent(t, suppressionKey, "msg_u"+n, email.EventBounced, "nobody-"+n+"@example.com"); status != http.StatusNoContent || body != "" {
		t.Fatalf("a signed bounce for an address Quizzivy does not hold: %d %q, want 204 with no body", status, body)
	}
	if status, body := w.deliverEvent(t, suppressionKey, "msg_b"+n, email.EventBounced, strings.ToUpper(bounced)); status != http.StatusNoContent || body != "" {
		t.Fatalf("a signed bounce: %d %q, want 204 with no body", status, body)
	}
	if status, body := w.deliverEvent(t, suppressionKey, "msg_b"+n, email.EventBounced, strings.ToUpper(bounced)); status != http.StatusNoContent || body != "" {
		t.Fatalf("the same bounce again: %d %q, want 204 with no body", status, body)
	}
	if status, body := w.deliverEvent(t, []byte("not-the-webhook-key-0123"), "msg_f"+n, email.EventComplained, forged); status != http.StatusUnauthorized || body != "" {
		t.Fatalf("a forged complaint: %d %q, want 401 with no body", status, body)
	}
	var listed []string
	rows, err := w.pool.Query(context.Background(), `SELECT address FROM app.email_suppressions WHERE address = ANY($1::text[])`, []string{bounced, forged, "nobody-" + n + "@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			t.Fatal(err)
		}
		listed = append(listed, a)
	}
	rows.Close()
	if len(listed) != 1 || listed[0] != bounced {
		t.Fatalf("listed %v, want the bounced address alone, lower-cased", listed)
	}

	dispatcher, sender := w.dispatcherAt(t, time.Now())
	drain(t, dispatcher)
	if got := sender.to(bounced); len(got) != 0 {
		t.Errorf("the bounced address was sent %d messages", len(got))
	}
	if got := sender.to(forged); len(got) != 1 {
		t.Errorf("the address a forged complaint named got %d messages, want its invite", len(got))
	}
	var code *string
	var secret bool
	if err := w.pool.QueryRow(context.Background(), `
		SELECT last_error_code, secret_ciphertext IS NOT NULL FROM app.email_outbox
		 WHERE to_address = $1 AND template = 'account_invite'`, bounced).Scan(&code, &secret); err != nil {
		t.Fatal(err)
	}
	if code == nil || *code != email.SuppressedCode || secret {
		t.Errorf("the invite to the bounced address ended with %v (secret kept %v), want SUPPRESSED and no secret", code, secret)
	}
}
