package email

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strings"

	"quizzivy/internal/platform/db"
)

// SuppressedCode is the last_error_code of a row whose recipient is on the
// suppression list (T-R7.15): closed as failed and never sent, as a row a
// Preparer dropped is.
const SuppressedCode = "SUPPRESSED"

// Reason is why an address is suppressed: its mail hard-bounced, or its
// owner marked a message as spam.
type Reason string

// The two reasons app.email_suppressions admits.
const (
	ReasonBounced    Reason = "bounced"
	ReasonComplained Reason = "complained"
)

// ErrNotAnAddress is a suppression whose address is not one email address.
var ErrNotAnAddress = errors.New("email: the suppressed address is not one email address")

// ErrUnknownAddress is a suppression of an address Quizzivy does not hold:
// no outbox row is to it and no account has it. Resend reports on everything
// its account sends, so an event can name an address that is not Quizzivy's
// to keep; nothing is stored for it.
var ErrUnknownAddress = errors.New("email: the suppressed address is not one Quizzivy holds")

// Suppression is an address to stop mailing, the reason, and the provider's
// id for the event that reported it.
type Suppression struct {
	Address string
	Reason  Reason
	EventID string
}

// SuppressionAddress is address as app.email_suppressions holds it and the
// dispatcher compares it: the bare address, trimmed and lower-cased. It
// answers false for anything that is not one address of 3 to 254 characters.
func SuppressionAddress(address string) (string, bool) {
	parsed, err := mail.ParseAddress(strings.TrimSpace(address))
	if err != nil || len(parsed.Address) < 3 || len(parsed.Address) > 254 {
		return "", false
	}
	return strings.ToLower(strings.TrimSpace(parsed.Address)), true
}

// Suppress lists s's address on q, if Quizzivy already holds the address: an
// app.email_outbox row is to it, in any case or padding, or an account has it
// as its email. It answers whether this call listed it: false when the
// address is already listed, under whichever reason came first, or when s's
// event was already recorded, so a replayed delivery changes nothing. An
// address Quizzivy does not hold is ErrUnknownAddress, and nothing is written.
//
// The outbox probe has no index on the address; the outbox keeps a day's
// budget of rows a day for Retention, a few thousand rows, and the webhook
// takes at most 120 events a minute. The account probe uses
// users_email_lower_key.
func Suppress(ctx context.Context, q db.Querier, s Suppression) (bool, error) {
	address, ok := SuppressionAddress(s.Address)
	if !ok {
		return false, ErrNotAnAddress
	}
	if s.Reason != ReasonBounced && s.Reason != ReasonComplained {
		return false, fmt.Errorf("email: unknown suppression reason %q", s.Reason)
	}
	if s.EventID == "" {
		return false, errors.New("email: a suppression needs the event's id")
	}
	var known, listed bool
	err := q.QueryRow(ctx, `
		WITH known AS (
		  SELECT EXISTS (SELECT 1 FROM app.email_outbox o WHERE lower(btrim(o.to_address)) = $1::text)
		      OR EXISTS (SELECT 1 FROM app.users u WHERE lower(u.email) = $1::text) AS yes
		), listed AS (
		  INSERT INTO app.email_suppressions (address, reason, provider_event_id)
		  SELECT $1::text, $2::text, $3::text FROM known WHERE known.yes
		  ON CONFLICT DO NOTHING
		  RETURNING 1
		)
		SELECT known.yes, EXISTS (SELECT 1 FROM listed) FROM known`, address, string(s.Reason), s.EventID).Scan(&known, &listed)
	if err != nil {
		return false, fmt.Errorf("email: suppress an address: %w", err)
	}
	if !known {
		return false, ErrUnknownAddress
	}
	return listed, nil
}
