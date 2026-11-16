package email

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"quizzivy/internal/platform/db"
)

// WebhookTolerance is how far a delivery's svix-timestamp may be from the
// receiver's clock, either way, before it is refused as stale: a replay of a
// captured delivery is refused once it is older than this.
const WebhookTolerance = 5 * time.Minute

// webhookSecretPrefix starts the signing secret Resend's dashboard shows.
const webhookSecretPrefix = "whsec_"

// minWebhookKey is the shortest decoded signing key accepted. Svix issues 24
// bytes; a shorter one is a truncated copy.
const minWebhookKey = 16

// The event types that suppress their recipient. Every other type is
// accepted and ignored.
const (
	EventBounced    = "email.bounced"
	EventComplained = "email.complained"
)

// BouncePermanent is the data.bounce.type of a hard bounce. Resend documents
// email.bounced as a permanent rejection, but its bounce types also include
// Transient (MailboxFull, MessageTooLarge, ContentRejected,
// AttachmentRejected) and Undetermined; a bounce that names any type but this
// one is not listed, since nothing lifts a suppression yet (O-R7.3). A bounce
// that names no type is taken as the documented hard bounce.
const BouncePermanent = "Permanent"

// ErrUnsigned is a delivery whose Svix signature is missing, malformed,
// wrong or stale. The webhook answers it 401 with no body.
var ErrUnsigned = errors.New("email: the delivery is not signed by the webhook's secret")

// ParseWebhookSecret decodes RESEND_WEBHOOK_SECRET: the "whsec_" Resend's
// dashboard shows, then the standard base64 of the signing key. The prefix
// may be left out, as Svix's own libraries allow.
func ParseWebhookSecret(raw string) ([]byte, error) {
	encoded := strings.TrimPrefix(strings.TrimSpace(raw), webhookSecretPrefix)
	key, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(key) < minWebhookKey {
		return nil, errors.New("RESEND_WEBHOOK_SECRET must be the signing secret Resend's dashboard shows for the webhook, whsec_ followed by base64")
	}
	return key, nil
}

// Delivery is one request to the webhook: its three Svix headers and its
// body, byte for byte as it arrived.
type Delivery struct {
	ID        string
	Timestamp string
	Signature string
	Body      []byte
}

// Webhook receives Resend's delivery events (T-R7.15). It accepts only a
// delivery signed with its key, and lists the recipient of a hard bounce or
// a complaint on app.email_suppressions, which R5's dispatcher reads before
// every send. Now is the clock, time.Now when nil.
type Webhook struct {
	key    []byte
	DB     db.Context
	Now    func() time.Time
	Logger *slog.Logger
}

// NewWebhook is the webhook verifying with secret, RESEND_WEBHOOK_SECRET as
// ParseWebhookSecret reads it.
func NewWebhook(secret string, dbx db.Context, logger *slog.Logger) (*Webhook, error) {
	key, err := ParseWebhookSecret(secret)
	if err != nil {
		return nil, err
	}
	return &Webhook{key: key, DB: dbx, Logger: logger}, nil
}

// Verify checks d's Svix signature: an HMAC-SHA256 with the webhook's key
// over "{svix-id}.{svix-timestamp}.{body}", compared in constant time with
// each "v1," signature svix-signature lists, the timestamp within
// WebhookTolerance of now. Any failure is ErrUnsigned.
func (w *Webhook) Verify(d Delivery) error {
	return VerifySignature(w.key, d, w.now())
}

// VerifySignature is Verify with key and now given.
func VerifySignature(key []byte, d Delivery, now time.Time) error {
	if len(key) == 0 || d.ID == "" || d.Timestamp == "" || d.Signature == "" {
		return ErrUnsigned
	}
	seconds, err := strconv.ParseInt(d.Timestamp, 10, 64)
	if err != nil {
		return ErrUnsigned
	}
	if skew := now.Sub(time.Unix(seconds, 0)); skew > WebhookTolerance || skew < -WebhookTolerance {
		return ErrUnsigned
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(d.ID + "." + d.Timestamp + "."))
	mac.Write(d.Body)
	expected := mac.Sum(nil)
	matched := false
	for _, entry := range strings.Fields(d.Signature) {
		version, encoded, ok := strings.Cut(entry, ",")
		if !ok || version != "v1" {
			continue
		}
		given, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			continue
		}
		if hmac.Equal(given, expected) {
			matched = true
		}
	}
	if !matched {
		return ErrUnsigned
	}
	return nil
}

// event is what the webhook reads of Resend's event: its type, its
// recipients and, for a bounce, the bounce's type. Everything else in it is
// ignored.
type event struct {
	Type string `json:"type"`
	Data struct {
		To     json.RawMessage `json:"to"`
		Bounce struct {
			Type string `json:"type"`
		} `json:"bounce"`
	} `json:"data"`
}

// Receive verifies d and, for a hard bounce or a complaint, lists its
// recipient, idempotent on d's svix-id, if Quizzivy holds the address (see
// Suppress). Every other event type, a bounce whose data.bounce.type is not
// Permanent, an address Quizzivy does not hold, and an event it cannot read
// are accepted and ignored: it is signed, so it is Resend's, and refusing it
// would only make Resend retry it. It answers ErrUnsigned for
// a delivery that fails Verify, and an error only when the list cannot be
// written, for Resend to retry. The log names the event's type, the reason
// and the recipient's domain, never the address.
func (w *Webhook) Receive(ctx context.Context, d Delivery) error {
	if err := w.Verify(d); err != nil {
		return err
	}
	var e event
	if err := json.Unmarshal(d.Body, &e); err != nil {
		w.Logger.Info("email event ignored", "type", "", "reason", "unreadable")
		return nil
	}
	var reason Reason
	switch e.Type {
	case EventBounced:
		if bounce := e.Data.Bounce.Type; bounce != "" && !strings.EqualFold(bounce, BouncePermanent) {
			w.Logger.Info("email event ignored", "type", e.Type, "reason", "soft_bounce", "bounceType", truncate(bounce, 64))
			return nil
		}
		reason = ReasonBounced
	case EventComplained:
		reason = ReasonComplained
	default:
		w.Logger.Debug("email event ignored", "type", truncate(e.Type, 64))
		return nil
	}
	address, ok := recipient(e.Data.To)
	if !ok {
		w.Logger.Info("email event ignored", "type", e.Type, "reason", "no_recipient")
		return nil
	}
	listed, err := Suppress(ctx, w.DB, Suppression{Address: address, Reason: reason, EventID: d.ID})
	if errors.Is(err, ErrUnknownAddress) {
		w.Logger.Info("email event ignored", "type", e.Type, "reason", "unknown_recipient", "toDomain", Domain(address))
		return nil
	}
	if err != nil {
		return err
	}
	w.Logger.Info("email address suppressed", "reason", string(reason), "toDomain", Domain(address), "new", listed)
	return nil
}

// recipient is the address an event names in data.to: Resend sends a list,
// and Quizzivy sends every message to one address, so the first one that
// parses is the message's. A plain string is read the same way.
func recipient(raw json.RawMessage) (string, bool) {
	var list []string
	if err := json.Unmarshal(raw, &list); err != nil {
		var one string
		if err := json.Unmarshal(raw, &one); err != nil {
			return "", false
		}
		list = []string{one}
	}
	for _, candidate := range list {
		if address, ok := SuppressionAddress(candidate); ok {
			return address, true
		}
	}
	return "", false
}

func (w *Webhook) now() time.Time {
	if w.Now == nil {
		return time.Now()
	}
	return w.Now()
}
