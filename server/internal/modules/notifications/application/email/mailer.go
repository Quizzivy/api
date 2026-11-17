// Package email is the notifications context's email channel (T-R7.13):
// Notify hands it each notice it writes, and it enqueues the email the notice
// calls for (domain.PlanEmail) unless NOTIFY_EMAIL_EVENTS turned that email
// off. The store decides the recipient's switch and writes R5's outbox; the
// emails' Preparers, which R5's dispatcher calls at send time, are adapters
// over the same store (core/adapters.NotificationPreparers), since only they
// may speak the email platform's types.
package email

import (
	"context"
	"time"

	"quizzivy/internal/modules/notifications/domain"
)

// Outbox is where an email is written: R5's email_outbox, in one transaction
// with the reads that decide it.
type Outbox interface {
	// Enqueue writes e when the email column of e.Switch (none for nil) is on
	// for e.UserID, or its default for whom they are, and they are enabled,
	// taking their address, and reports whether it wrote a row. A thread's
	// email is spaced as domain.Email says.
	Enqueue(ctx context.Context, e domain.Email) (bool, error)
}

// Mailer is Notify's email channel. Sends answers whether NOTIFY_EMAIL_EVENTS
// leaves an email on, by its domain.EmailTemplate.Event name; a nil Sends
// sends every one.
type Mailer struct {
	Outbox Outbox
	Sends  func(event string) bool
}

// Mail enqueues n's email, written at now, and does nothing for a notice that
// sends none or whose email is turned off.
func (m Mailer) Mail(ctx context.Context, n domain.Notice, now time.Time) error {
	e, ok := domain.PlanEmail(n, now)
	if !ok || (m.Sends != nil && !m.Sends(e.Template.Event())) {
		return nil
	}
	_, err := m.Outbox.Enqueue(ctx, e)
	return err
}
