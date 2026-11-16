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

// Suppress lists s's address on q. It answers whether this call listed it:
// false when the address is already listed, under whichever reason came
// first, or when s's event was already recorded, so a replayed delivery
// changes nothing.
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
	tag, err := q.Exec(ctx, `
		INSERT INTO app.email_suppressions (address, reason, provider_event_id)
		VALUES ($1, $2, $3)
		ON CONFLICT DO NOTHING`, address, string(s.Reason), s.EventID)
	if err != nil {
		return false, fmt.Errorf("email: suppress an address: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}
