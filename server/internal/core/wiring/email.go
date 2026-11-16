package wiring

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"quizzivy/internal/core/adapters"
	"quizzivy/internal/core/jobs"
	notificationsemail "quizzivy/internal/modules/notifications/application/email"
	notificationsrepo "quizzivy/internal/modules/notifications/repositories"
	organizationmodel "quizzivy/internal/modules/organization/application/model"
	organizationquery "quizzivy/internal/modules/organization/application/query"
	"quizzivy/internal/platform/config"
	"quizzivy/internal/platform/db"
	"quizzivy/internal/platform/email"
	"quizzivy/internal/platform/secrets"
	"quizzivy/internal/shared/cqrs"
)

const (
	fallbackOrganizationName = "Quizzivy"
	resendTimeout            = 15 * time.Second
	emailRetry               = time.Minute
	emailBusy                = 10 * time.Second
	emailFloor               = time.Second
)

// Mail is the outgoing mail of one process: the outbox its modules enqueue
// into and the dispatcher that sends the outbox's rows. Dispatcher is nil
// when the process has no sender; the API and the worker both build one.
type Mail struct {
	Outbox     *email.Outbox
	Dispatcher *email.Dispatcher
}

// Run dispatches until ctx ends, on this process's enqueues and on the
// earliest pending row's due time.
func (m Mail) Run(ctx context.Context, logger *slog.Logger) {
	jobs.RunEmail(ctx, logger, m.Dispatcher, jobs.EmailSchedule{
		Wake:  m.Outbox.Wake(),
		Retry: emailRetry, Busy: emailBusy, Floor: emailFloor,
	})
}

// Branding is the organization's branding read as it heads every email.
type Branding = cqrs.QueryHandler[organizationquery.Branding, organizationmodel.Branding]

// BuildMail wires the email platform over cfg: Resend when its group is
// configured and the sink directory otherwise, which the configuration refuses
// in production. A message's link points into the web host of cfg.Hosts its
// recipient belongs at. Every header names the centre by branding's short name, else
// its name, else Quizzivy; until the settings have been read once, a batch
// fails and logs ORGANIZATION_SETTINGS_UNAVAILABLE. A missing SECRETS_KEY leaves the outbox unable to
// seal a secret and every sealed row failing closed. The dispatcher holds the
// notification emails' Preparers (T-R7.13), so the API's and the worker's
// both send them, and it logs which of them NOTIFY_EMAIL_EVENTS leaves on.
func BuildMail(cfg config.Config, dbx db.Context, branding Branding, logger *slog.Logger) (Mail, error) {
	var sealer *secrets.Sealer
	if cfg.SecretsKey != nil {
		var err error
		if sealer, err = secrets.New(cfg.SecretsKey, secrets.PurposeEmailOutbox); err != nil {
			return Mail{}, fmt.Errorf("SECRETS_KEY: %w", err)
		}
	}
	renderer, err := email.NewRenderer()
	if err != nil {
		return Mail{}, err
	}
	var sender email.Sender = email.Sink{Dir: cfg.Email.SinkDir, Logger: logger}
	if cfg.Email.Enabled() {
		sender = email.Resend{APIKey: cfg.Email.APIKey.Reveal(), From: cfg.Email.From, Client: &http.Client{Timeout: resendTimeout}}
	} else {
		logger.Warn("email goes to the sink directory, not to a provider", "dir", cfg.Email.SinkDir)
	}
	logger.Info("notification emails", "events", cfg.Email.NotifyEvents())
	return Mail{
		Outbox: email.NewOutbox(sealer),
		Dispatcher: &email.Dispatcher{
			DB: dbx, Sealer: sealer, Sender: sender, Renderer: renderer,
			Org: organizationName(branding, logger), Hosts: cfg.Hosts, ReplyTo: cfg.Email.ReplyTo,
			Limits: cfg.Limits, Interval: cfg.Email.SendInterval(),
			Location: cfg.AppLocation, Logger: logger,
			Prepare: adapters.NotificationPreparers(notificationsrepo.NewMail(dbx, nil), cfg.Email.Sends),
		},
	}, nil
}

// EmailWebhook is Resend's webhook over cfg's RESEND_WEBHOOK_SECRET, which
// lists the recipients of hard bounces and complaints for the dispatcher to
// skip (T-R7.15). Without the secret it is nil, receiveEmailEvents answers
// 501, and the API logs that bounces and complaints are not received.
func EmailWebhook(cfg config.Config, dbx db.Context, logger *slog.Logger) (*email.Webhook, error) {
	if !cfg.Email.ReceivesEvents() {
		logger.Warn("email bounces and complaints are not received; POST /public/email-events answers 501", "setting", "RESEND_WEBHOOK_SECRET")
		return nil, nil
	}
	webhook, err := email.NewWebhook(cfg.Email.WebhookSecret.Reveal(), dbx, logger)
	if err != nil {
		return nil, err
	}
	logger.Info("email bounces and complaints are received", "events", []string{email.EventBounced, email.EventComplained})
	return webhook, nil
}

func organizationName(branding Branding, logger *slog.Logger) func(context.Context) (string, error) {
	return func(ctx context.Context) (string, error) {
		b, err := branding.Handle(ctx, organizationquery.Branding{})
		if err != nil {
			logger.Warn("organization settings unavailable; email waits for them", "code", "ORGANIZATION_SETTINGS_UNAVAILABLE")
			return "", err
		}
		for _, name := range []string{b.ShortName, b.Name} {
			if name != "" {
				return name, nil
			}
		}
		return fallbackOrganizationName, nil
	}
}

// NotificationMail is the notifications module's email channel over mail's
// outbox: Notify enqueues each notice's email there, under the recipient's
// switch and NOTIFY_EMAIL_EVENTS.
func NotificationMail(cfg config.Config, dbx db.Context, mail Mail) notificationsemail.Mailer {
	return notificationsemail.Mailer{Outbox: notificationsrepo.NewMail(dbx, mail.Outbox), Sends: cfg.Email.Sends}
}
