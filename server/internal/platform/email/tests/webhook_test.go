package email_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"testing"
	"time"

	"quizzivy/internal/platform/db"
	"quizzivy/internal/platform/email"
)

// signingKey and webhookSecret are a Resend-style signing secret: "whsec_"
// and the standard base64 of 24 bytes.
var (
	signingKey    = []byte("0123456789abcdef01234567")
	webhookSecret = "whsec_" + base64.StdEncoding.EncodeToString(signingKey)
	webhookNow    = time.Date(2031, 3, 10, 5, 0, 0, 0, time.UTC)
)

// signed is a delivery of body as Svix signs it: an HMAC-SHA256 with key over
// "{id}.{timestamp}.{body}", sent as "v1,<base64>".
func signed(key []byte, id string, at time.Time, body string) email.Delivery {
	ts := strconv.FormatInt(at.Unix(), 10)
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(id + "." + ts + "." + body))
	return email.Delivery{ID: id, Timestamp: ts, Signature: "v1," + base64.StdEncoding.EncodeToString(mac.Sum(nil)), Body: []byte(body)}
}

const bounceBody = `{"type":"email.bounced","created_at":"2031-03-10T05:00:00.000Z","data":{"email_id":"4ef9a417","to":["Nam.Le@Example.com"],"subject":"x"}}`

// TestOnlyADeliverySignedWithTheSecretInsideFiveMinutesIsVerified fails when
// Verify accepts a wrong secret, a stale or future timestamp, a body, id or
// timestamp other than the signed one, a missing header, or a signature in
// another scheme than v1; or when it refuses a valid one among several, as
// Svix sends while a secret is rotated.
func TestOnlyADeliverySignedWithTheSecretInsideFiveMinutesIsVerified(t *testing.T) {
	good := signed(signingKey, "msg_2Lh9KJp", webhookNow, bounceBody)
	if err := email.VerifySignature(signingKey, good, webhookNow); err != nil {
		t.Fatalf("a valid delivery was refused: %v", err)
	}
	for _, skew := range []time.Duration{-email.WebhookTolerance, email.WebhookTolerance} {
		if err := email.VerifySignature(signingKey, good, webhookNow.Add(skew)); err != nil {
			t.Errorf("a delivery %v from now, inside the tolerance, was refused: %v", skew, err)
		}
	}
	rotating := good
	rotating.Signature = "v1,AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA= v1,not-base64 " + good.Signature
	if err := email.VerifySignature(signingKey, rotating, webhookNow); err != nil {
		t.Errorf("a valid signature listed after others was refused: %v", err)
	}

	tampered := good
	tampered.Body = []byte(strings.Replace(bounceBody, "Nam.Le", "someone.else", 1))
	otherID, otherTime := good, good
	otherID.ID = "msg_other"
	otherTime.Timestamp = strconv.FormatInt(webhookNow.Unix()+1, 10)
	v2 := good
	v2.Signature = "v2," + strings.TrimPrefix(good.Signature, "v1,")
	cases := map[string]struct {
		key []byte
		d   email.Delivery
		at  time.Time
	}{
		"the wrong secret":          {[]byte("another-secret-of-24-byte"), good, webhookNow},
		"a stale timestamp":         {signingKey, good, webhookNow.Add(email.WebhookTolerance + time.Second)},
		"a timestamp in the future": {signingKey, good, webhookNow.Add(-email.WebhookTolerance - time.Second)},
		"a tampered body":           {signingKey, tampered, webhookNow},
		"another delivery's id":     {signingKey, otherID, webhookNow},
		"another timestamp":         {signingKey, otherTime, webhookNow},
		"a v2 signature":            {signingKey, v2, webhookNow},
		"no id":                     {signingKey, email.Delivery{Timestamp: good.Timestamp, Signature: good.Signature, Body: good.Body}, webhookNow},
		"no timestamp":              {signingKey, email.Delivery{ID: good.ID, Signature: good.Signature, Body: good.Body}, webhookNow},
		"no signature":              {signingKey, email.Delivery{ID: good.ID, Timestamp: good.Timestamp, Body: good.Body}, webhookNow},
		"a timestamp that is text":  {signingKey, email.Delivery{ID: good.ID, Timestamp: "soon", Signature: good.Signature, Body: good.Body}, webhookNow},
		"no key":                    {nil, good, webhookNow},
	}
	for name, c := range cases {
		if err := email.VerifySignature(c.key, c.d, c.at); !errors.Is(err, email.ErrUnsigned) {
			t.Errorf("%s: %v, want ErrUnsigned", name, err)
		}
	}
}

// TestTheWebhookSecretIsReadAsResendsDashboardShowsIt fails when the
// "whsec_" form, or its base64 alone, is not decoded to the signing key, or
// when a value that is no key is accepted.
func TestTheWebhookSecretIsReadAsResendsDashboardShowsIt(t *testing.T) {
	for _, raw := range []string{webhookSecret, strings.TrimPrefix(webhookSecret, "whsec_")} {
		key, err := email.ParseWebhookSecret(raw)
		if err != nil || string(key) != string(signingKey) {
			t.Errorf("%q: %q %v", raw, key, err)
		}
	}
	for _, raw := range []string{"", "whsec_", "whsec_***", "whsec_" + base64.StdEncoding.EncodeToString([]byte("short"))} {
		if _, err := email.ParseWebhookSecret(raw); err == nil {
			t.Errorf("%q was accepted", raw)
		}
	}
}

// TestAnUnsignedDeliveryIsRefusedBeforeTheDatabase fails when Receive reads or
// writes anything for a delivery that fails the signature: the webhook's
// database here is one no query can reach.
func TestAnUnsignedDeliveryIsRefusedBeforeTheDatabase(t *testing.T) {
	webhook, err := email.NewWebhook(webhookSecret, db.Context{}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	webhook.Now = func() time.Time { return webhookNow }
	forged := signed([]byte("not-the-secret-0123456789"), "msg_forged", webhookNow, bounceBody)
	if err := webhook.Receive(context.Background(), forged); !errors.Is(err, email.ErrUnsigned) {
		t.Errorf("a forged bounce: %v, want ErrUnsigned", err)
	}
	stale := signed(signingKey, "msg_stale", webhookNow.Add(-time.Hour), bounceBody)
	if err := webhook.Receive(context.Background(), stale); !errors.Is(err, email.ErrUnsigned) {
		t.Errorf("a replayed hour-old bounce: %v, want ErrUnsigned", err)
	}
	for _, body := range []string{
		`{"type":"email.delivered","data":{"to":["a@example.com"]}}`,
		`{"type":"email.bounced","data":{"to":[]}}`,
		`{"type":"email.complained","data":{"to":["not an address"]}}`,
		`{"type":"email.bounced"}`,
		`{"data":{"to":["a@example.com"]}}`,
	} {
		if err := webhook.Receive(context.Background(), signed(signingKey, "msg_"+body[:12], webhookNow, body)); err != nil {
			t.Errorf("a signed event with nothing to list (%s): %v, want it accepted and ignored", body, err)
		}
	}
}

// TestASuppressedAddressIsStoredAsTheDispatcherComparesIt fails when the
// list's form of an address is not the trimmed, lower-cased bare address, or
// when something that is not one address gets a form.
func TestASuppressedAddressIsStoredAsTheDispatcherComparesIt(t *testing.T) {
	for raw, want := range map[string]string{
		"Nam.Le@Example.com":          "nam.le@example.com",
		"  nam.le@example.com \t":     "nam.le@example.com",
		"Nam Le <Nam.Le@Example.com>": "nam.le@example.com",
	} {
		if got, ok := email.SuppressionAddress(raw); !ok || got != want {
			t.Errorf("%q: %q %v, want %q", raw, got, ok, want)
		}
	}
	for _, raw := range []string{"", "nobody", "a@b.com, c@d.com", strings.Repeat("a", 250) + "@example.com"} {
		if got, ok := email.SuppressionAddress(raw); ok {
			t.Errorf("%q was given the form %q", raw, got)
		}
	}
}
