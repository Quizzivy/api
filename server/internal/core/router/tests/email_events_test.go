package router_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"quizzivy/internal/core/router"
	"quizzivy/internal/platform/db"
	"quizzivy/internal/platform/email"
)

var (
	webhookKey    = []byte("0123456789abcdef01234567")
	webhookSecret = "whsec_" + base64.StdEncoding.EncodeToString(webhookKey)
)

// webhookRouter is the router with Resend's webhook on. Its database is one
// no query can reach, so only deliveries that list nothing may get past the
// signature check here; the database tests cover the list.
func webhookRouter(t *testing.T) http.Handler {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	webhook, err := email.NewWebhook(webhookSecret, db.Context{}, logger)
	if err != nil {
		t.Fatal(err)
	}
	h, err := router.New(router.Deps{Principals: newFakePrincipals(), DB: fakeDB{}, EmailWebhook: webhook}, logger, []string{"https://app.quizzivy.com"}, "")
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// delivery is a POST to the webhook of body, signed with key at at under id
// (unsigned when key is nil), from addr.
func delivery(key []byte, id string, at time.Time, body, addr string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/public/email-events", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = addr
	if key != nil {
		ts := strconv.FormatInt(at.Unix(), 10)
		mac := hmac.New(sha256.New, key)
		mac.Write([]byte(id + "." + ts + "." + body))
		req.Header.Set("svix-id", id)
		req.Header.Set("svix-timestamp", ts)
		req.Header.Set("svix-signature", "v1,"+base64.StdEncoding.EncodeToString(mac.Sum(nil)))
	}
	return req
}

const deliveredEvent = `{"type":"email.delivered","created_at":"2031-03-10T05:00:00.000Z","data":{"to":["a@example.com"]}}`

// TestTheWebhookAnswers204OrAnEmpty401 fails when a delivery signed with the
// secret is not answered 204 with no body, when any failure of the signature
// (no headers, wrong secret, stale, tampered, the body re-encoded) is
// answered other than 401 with no body, or when the signature is checked
// over anything but the bytes that arrived.
func TestTheWebhookAnswers204OrAnEmpty401(t *testing.T) {
	h := webhookRouter(t)
	now := time.Now()
	// Spacing and key order the generated handler's decoding would not keep.
	odd := "{ \"data\" : {\"to\": [\"a@example.com\"]},\n  \"type\":\"email.opened\" }"
	for name, req := range map[string]*http.Request{
		"a delivered event":        delivery(webhookKey, "msg_1", now, deliveredEvent, "203.0.113.40:1"),
		"an event read as it came": delivery(webhookKey, "msg_2", now, odd, "203.0.113.40:1"),
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusNoContent || rec.Body.Len() != 0 {
			t.Errorf("%s: %d %q, want 204 with no body", name, rec.Code, rec.Body.String())
		}
	}

	tampered := delivery(webhookKey, "msg_3", now, deliveredEvent, "203.0.113.40:1")
	tampered.Body = io.NopCloser(strings.NewReader(strings.Replace(deliveredEvent, "a@example.com", "b@example.com", 1)))
	tampered.ContentLength = -1
	noSignature := delivery(webhookKey, "msg_4", now, deliveredEvent, "203.0.113.40:1")
	noSignature.Header.Del("svix-signature")
	for name, req := range map[string]*http.Request{
		"no headers":       delivery(nil, "", now, deliveredEvent, "203.0.113.40:1"),
		"no signature":     noSignature,
		"the wrong secret": delivery([]byte("another-secret-of-24-byte"), "msg_5", now, deliveredEvent, "203.0.113.40:1"),
		"a stale delivery": delivery(webhookKey, "msg_6", now.Add(-6*time.Minute), deliveredEvent, "203.0.113.40:1"),
		"a future one":     delivery(webhookKey, "msg_7", now.Add(6*time.Minute), deliveredEvent, "203.0.113.40:1"),
		"a tampered body":  tampered,
		"a forged bounce":  delivery([]byte("another-secret-of-24-byte"), "msg_8", now, `{"type":"email.bounced","data":{"to":["victim@example.com"]}}`, "203.0.113.40:1"),
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized || rec.Body.Len() != 0 {
			t.Errorf("%s: %d %q, want 401 with no body", name, rec.Code, rec.Body.String())
		}
	}
}

// TestTheWebhookRefusesWhatIsNoEventBeforeTheSignature fails when a body that
// is not a JSON object, or one over 64 KiB, reaches the signature check: the
// contract answers them 400 and 413, as on every operation.
func TestTheWebhookRefusesWhatIsNoEventBeforeTheSignature(t *testing.T) {
	h := webhookRouter(t)
	now := time.Now()
	for name, c := range map[string]struct {
		body string
		want int
	}{
		"a list":            {`["email.bounced"]`, http.StatusBadRequest},
		"not JSON":          {`type=email.bounced`, http.StatusBadRequest},
		"over 64 KiB":       {`{"type":"email.delivered","pad":"` + strings.Repeat("x", 64<<10) + `"}`, http.StatusRequestEntityTooLarge},
		"just under 64 KiB": {`{"type":"email.delivered","pad":"` + strings.Repeat("x", (64<<10)-40) + `"}`, http.StatusNoContent},
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, delivery(webhookKey, "msg_"+name, now, c.body, "203.0.113.41:1"))
		if rec.Code != c.want {
			t.Errorf("%s: %d %s, want %d", name, rec.Code, rec.Body.String(), c.want)
		}
	}
}

// TestWithoutTheSecretTheWebhookAnswers501 fails when a deployment without
// RESEND_WEBHOOK_SECRET answers a delivery other than 501 in the error
// envelope, signed or not: without the secret nothing can be verified.
func TestWithoutTheSecretTheWebhookAnswers501(t *testing.T) {
	h := newTestRouter(t, fakeDB{})
	for name, key := range map[string][]byte{"signed": webhookKey, "unsigned": nil} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, delivery(key, "msg_9", time.Now(), deliveredEvent, "203.0.113.42:1"))
		if rec.Code != http.StatusNotImplemented || errorCode(t, rec) != "INTERNAL" {
			t.Errorf("%s: %d, want 501 INTERNAL", name, rec.Code)
		}
	}
}

// TestTheWebhookTakes120DeliveriesAMinuteFromOneAddress fails when its
// per-address budget is not 120 a minute, or when signed deliveries escape
// it, or when one address's budget is spent for another.
func TestTheWebhookTakes120DeliveriesAMinuteFromOneAddress(t *testing.T) {
	h := webhookRouter(t)
	now := time.Now()
	for i := 1; i <= 121; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, delivery(webhookKey, "msg_r"+strconv.Itoa(i), now, deliveredEvent, "203.0.113.43:1"))
		switch {
		case i <= 120 && rec.Code != http.StatusNoContent:
			t.Fatalf("delivery %d: %d, want 204 within the budget", i, rec.Code)
		case i == 121 && (rec.Code != http.StatusTooManyRequests || errorCode(t, rec) != "RATE_LIMITED"):
			t.Fatalf("delivery 121: %d, want 429 RATE_LIMITED", rec.Code)
		}
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, delivery(webhookKey, "msg_other", now, deliveredEvent, "203.0.113.44:1"))
	if rec.Code != http.StatusNoContent {
		t.Errorf("another address after the first's budget: %d, want 204", rec.Code)
	}
	route, ok := router.RateLimits().Lookup("POST /public/email-events")
	if !ok || route.PerIP == nil || len(route.PerIP.Rules()) != 1 || route.PerIP.Rules()[0].Burst != 120 {
		t.Errorf("the webhook's limit is %+v", route)
	}
}
