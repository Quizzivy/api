package wiring_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"quizzivy/internal/core/wiring"
	organizationmodel "quizzivy/internal/modules/organization/application/model"
	organizationquery "quizzivy/internal/modules/organization/application/query"
	"quizzivy/internal/platform/config"
	"quizzivy/internal/platform/db"
)

type branding struct {
	answer organizationmodel.Branding
	err    error
}

func (b branding) Handle(context.Context, organizationquery.Branding) (organizationmodel.Branding, error) {
	return b.answer, b.err
}

func headerName(t *testing.T, b branding) (string, error) {
	t.Helper()
	return headerNameLogging(t, b, io.Discard)
}

func headerNameLogging(t *testing.T, b branding, logs io.Writer) (string, error) {
	t.Helper()
	cfg := config.Config{Email: config.Email{SinkDir: t.TempDir()}}
	mail, err := wiring.BuildMail(cfg, db.NewContext(nil), b, slog.New(slog.NewJSONHandler(logs, nil)))
	if err != nil {
		t.Fatalf("BuildMail: %v", err)
	}
	return mail.Dispatcher.Org(context.Background())
}

func TestEmailHeaderNamesTheCentre(t *testing.T) {
	cases := []struct {
		name string
		in   organizationmodel.Branding
		want string
	}{
		{"the short name first", organizationmodel.Branding{Name: "Trung tâm Anh ngữ Ánh Dương", ShortName: "Ánh Dương"}, "Ánh Dương"},
		{"the name without a short one", organizationmodel.Branding{Name: "Trung tâm Anh ngữ Ánh Dương"}, "Trung tâm Anh ngữ Ánh Dương"},
		{"Quizzivy without either", organizationmodel.Branding{}, "Quizzivy"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := headerName(t, branding{answer: c.in})
			if err != nil {
				t.Fatalf("Org: %v", err)
			}
			if got != c.want {
				t.Errorf("Org = %q, want %q", got, c.want)
			}
		})
	}
}

func TestEmailHeaderPassesOnAFailedSettingsReadAndLogsItsCode(t *testing.T) {
	failed := errors.New("password=hunter2 refused")
	var logs strings.Builder
	if _, err := headerNameLogging(t, branding{err: failed}, &logs); !errors.Is(err, failed) {
		t.Fatalf("Org error = %v, want %v", err, failed)
	}
	if out := logs.String(); !strings.Contains(out, `"code":"ORGANIZATION_SETTINGS_UNAVAILABLE"`) || strings.Contains(out, "hunter2") {
		t.Fatalf("log: %s", out)
	}
}

// TestTheWebhookIsBuiltOnlyWithItsSecretAndNeverLogsIt fails when a
// deployment without RESEND_WEBHOOK_SECRET gets a webhook (it must answer
// 501) or says nothing about it, when one with the secret gets none, or when
// either log carries the secret.
func TestTheWebhookIsBuiltOnlyWithItsSecretAndNeverLogsIt(t *testing.T) {
	var logs strings.Builder
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	webhook, err := wiring.EmailWebhook(config.Config{}, db.NewContext(nil), logger)
	if err != nil || webhook != nil {
		t.Fatalf("without the secret: %v %v, want no webhook", webhook, err)
	}
	if !strings.Contains(logs.String(), "RESEND_WEBHOOK_SECRET") {
		t.Errorf("without the secret nothing is logged: %s", logs.String())
	}
	secret := "whsec_MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3"
	logs.Reset()
	webhook, err = wiring.EmailWebhook(config.Config{Email: config.Email{WebhookSecret: config.Secret(secret)}}, db.NewContext(nil), logger)
	if err != nil || webhook == nil {
		t.Fatalf("with the secret: %v %v, want a webhook", webhook, err)
	}
	if out := logs.String(); strings.Contains(out, "MDEyMzQ1") || !strings.Contains(out, "email.bounced") {
		t.Errorf("the log carries the secret, or not the events received: %s", out)
	}
}
