package config_test

import (
	"encoding/base64"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"quizzivy/internal/platform/config"
	"quizzivy/internal/platform/email"
)

var emailVariables = []string{
	"RESEND_API_KEY", "EMAIL_FROM", "EMAIL_REPLY_TO", "EMAIL_DAILY_LIMIT", "EMAIL_SEND_RATE",
	"EMAIL_SINK_DIR", "SECRETS_KEY", "APP_TIMEZONE", "NOTIFY_EMAIL_EVENTS", "RESEND_WEBHOOK_SECRET",
}

var secretsKey = base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))

func fullEmail() map[string]string {
	return map[string]string{
		"RESEND_API_KEY": "re_secret_value", "EMAIL_FROM": "Quizzivy <no-reply@quizzivy.com>",
		"SECRETS_KEY": secretsKey,
	}
}

func TestWithoutTheEmailGroupOutsideProductionTheSinkTakesTheMail(t *testing.T) {
	cfg, err := loadWith(t, nil)
	if err != nil {
		t.Fatal(err)
	}
	e := cfg.Email
	if e.Enabled() || e.SinkDir != "tmp/email-sink" || e.DailyLimit != 100 || e.SendRate != 2 || e.SendInterval() != 500*time.Millisecond {
		t.Fatalf("defaults: %+v", e)
	}
	if cfg.SecretsKey != nil || cfg.AppLocation == nil || cfg.AppLocation.String() != "Asia/Ho_Chi_Minh" {
		t.Fatalf("secrets key %v, location %v", cfg.SecretsKey, cfg.AppLocation)
	}
}

func TestTheEmailGroupLoadsAndKeepsTheKeyOutOfEveryPrintedForm(t *testing.T) {
	cfg, err := loadWith(t, merge(fullEmail(), map[string]string{
		"EMAIL_REPLY_TO": "frontdesk@quizzivy.com", "EMAIL_DAILY_LIMIT": "500", "EMAIL_SEND_RATE": "5",
		"APP_TIMEZONE": "Asia/Bangkok",
	}))
	if err != nil {
		t.Fatal(err)
	}
	e := cfg.Email
	if !e.Enabled() || e.From != "Quizzivy <no-reply@quizzivy.com>" || e.ReplyTo != "frontdesk@quizzivy.com" ||
		e.DailyLimit != 500 || e.SendRate != 5 || e.SendInterval() != 200*time.Millisecond || len(cfg.SecretsKey) != 32 || cfg.AppLocation.String() != "Asia/Bangkok" {
		t.Fatalf("loaded: %+v", e)
	}
	if e.APIKey.Reveal() != "re_secret_value" {
		t.Fatal("the key is lost")
	}
	if printed := fmt.Sprintf("%v %+v %#v", e.APIKey, e, e); strings.Contains(printed, "re_secret_value") {
		t.Errorf("the key is printed: %s", printed)
	}
}

func TestAPartOfTheEmailGroupIsRefusedByName(t *testing.T) {
	for _, drop := range []string{"RESEND_API_KEY", "EMAIL_FROM"} {
		env := fullEmail()
		env[drop] = ""
		_, err := loadWith(t, env)
		if err == nil || !strings.Contains(err.Error(), "RESEND_API_KEY and EMAIL_FROM together") {
			t.Errorf("without %s: %v", drop, err)
		}
	}
}

func TestProductionRefusesTheSinkAndAPartialOrMissingGroup(t *testing.T) {
	production := merge(productionHosts(), map[string]string{"SECRETS_KEY": secretsKey})
	if _, err := loadWith(t, production); err == nil || !strings.Contains(err.Error(), "RESEND_API_KEY and EMAIL_FROM are required in production") {
		t.Errorf("production without the group: %v", err)
	}
	if _, err := loadWith(t, merge(merge(fullEmail(), production), map[string]string{"EMAIL_SINK_DIR": "/tmp/sink"})); err == nil || !strings.Contains(err.Error(), "EMAIL_SINK_DIR") {
		t.Errorf("production with the sink: %v", err)
	}
	noKey := merge(fullEmail(), production)
	noKey["SECRETS_KEY"] = ""
	if _, err := loadWith(t, noKey); err == nil || !strings.Contains(err.Error(), "SECRETS_KEY is required in production") {
		t.Errorf("production without SECRETS_KEY: %v", err)
	}
	if _, err := loadWith(t, merge(fullEmail(), production)); err != nil {
		t.Errorf("production with everything: %v", err)
	}
}

func TestBadEmailValuesAreRefusedWithoutRepeatingTheKey(t *testing.T) {
	for name, override := range map[string]map[string]string{
		"a sender that is no mail":   {"EMAIL_FROM": "garbage"},
		"a sender with a line break": {"EMAIL_FROM": "Q <a@b.com>\r\nBcc: x@y.com"},
		"two senders":                {"EMAIL_FROM": "a@b.com, c@d.com"},
		"a reply-to of only @":       {"EMAIL_REPLY_TO": "@"},
		"a reply-to with a line":     {"EMAIL_REPLY_TO": "a@b.com\r\nBcc: x@y.com"},
		"a key with whitespace":      {"RESEND_API_KEY": "re_secret value"},
		"a zero daily limit":         {"EMAIL_DAILY_LIMIT": "0"},
		"a daily limit that is text": {"EMAIL_DAILY_LIMIT": "many"},
		"a zero send rate":           {"EMAIL_SEND_RATE": "0"},
		"a send rate over 100":       {"EMAIL_SEND_RATE": "101"},
		"a reply-to that is no mail": {"EMAIL_REPLY_TO": "front desk"},
		"a secrets key not base64":   {"SECRETS_KEY": "not-base64!"},
		"a short secrets key":        {"SECRETS_KEY": base64.StdEncoding.EncodeToString([]byte("short"))},
		"an unknown timezone":        {"APP_TIMEZONE": "Mars/Olympus"},
	} {
		_, err := loadWith(t, merge(fullEmail(), override))
		if err == nil {
			t.Errorf("%s was accepted", name)
			continue
		}
		for _, secret := range []string{"re_secret", secretsKey, override["SECRETS_KEY"]} {
			if secret != "" && strings.Contains(err.Error(), secret) {
				t.Errorf("%s: the error repeats a secret: %v", name, err)
			}
		}
	}
}

func TestTheImportWorkerLoadsTheSameEmailGroup(t *testing.T) {
	workerEnvironment(t, merge(fullEmail(), map[string]string{"APP_ENV": "production", "APP_TIMEZONE": "Asia/Bangkok"}))
	cfg, err := config.LoadImportWorker()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Base.Email.Enabled() || len(cfg.Base.SecretsKey) != 32 || cfg.Base.AppLocation.String() != "Asia/Bangkok" {
		t.Fatalf("worker: %+v", cfg.Base.Email)
	}
	workerEnvironment(t, map[string]string{"APP_ENV": "production"})
	if _, err := config.LoadImportWorker(); err == nil {
		t.Error("a production worker without the email group started")
	}
}

// TestNotifyEmailEventsTurnsEachNotificationEmailOffByName fails when the
// kill switch's default stops being every notification email, when "none"
// sends something, or when a name it does not know is accepted (a typo would
// leave an email on).
func TestNotifyEmailEventsTurnsEachNotificationEmailOffByName(t *testing.T) {
	cfg, err := loadWith(t, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(cfg.Email.Events, email.NotifyEvents()) || !cfg.Email.Sends("message.received") {
		t.Errorf("unset, the events are %v, want all of %v", cfg.Email.Events, email.NotifyEvents())
	}
	for raw, want := range map[string][]string{
		"   ":             email.NotifyEvents(),
		"none":            {},
		"attempt.flagged": {"attempt.flagged"},
		" result.ready , message.received,,result.ready ": {"result.ready", "message.received"},
	} {
		cfg, err := loadWith(t, map[string]string{"NOTIFY_EMAIL_EVENTS": raw})
		if err != nil {
			t.Fatalf("%q: %v", raw, err)
		}
		if !slices.Equal(cfg.Email.Events, want) {
			t.Errorf("%q gives %v, want %v", raw, cfg.Email.Events, want)
		}
	}
	if !(config.Email{}).Sends("summary.weekly") || !slices.Equal((config.Email{}).NotifyEvents(), email.NotifyEvents()) {
		t.Error("a Config built without Load sends no notification email")
	}
	if none, _ := loadWith(t, map[string]string{"NOTIFY_EMAIL_EVENTS": "none"}); none.Email.Sends("attempt.flagged") || len(none.Email.NotifyEvents()) != 0 {
		t.Error("none still sends a notification email")
	}
	cfg, _ = loadWith(t, map[string]string{"NOTIFY_EMAIL_EVENTS": "attempt.flagged"})
	if cfg.Email.Sends("message.received") || !cfg.Email.Sends("attempt.flagged") {
		t.Errorf("Sends does not follow the list: %v", cfg.Email.Events)
	}
	for _, raw := range []string{"message.recieved", "attempt.flagged,all", "message.from_teacher"} {
		if _, err := loadWith(t, map[string]string{"NOTIFY_EMAIL_EVENTS": raw}); err == nil || !strings.Contains(err.Error(), "NOTIFY_EMAIL_EVENTS") {
			t.Errorf("%q: err = %v, want NOTIFY_EMAIL_EVENTS refused", raw, err)
		}
	}
}

// webhookSecret is a signing secret in the form Resend's dashboard shows.
var webhookSecret = "whsec_" + base64.StdEncoding.EncodeToString([]byte("0123456789abcdef01234567"))

// TestTheWebhookSecretIsOptionalCheckedAndNeverPrinted fails when an unset
// RESEND_WEBHOOK_SECRET stops the API (it only turns the webhook off; the
// committed production config boots without it, as
// TestTheCommittedProductionConfigBootsWithTheDocumentedSecrets shows),
// when a secret that is not a signing key starts one, or when the secret
// reaches an error or a printed config.
func TestTheWebhookSecretIsOptionalCheckedAndNeverPrinted(t *testing.T) {
	cfg, err := loadWith(t, fullEmail())
	if err != nil {
		t.Fatalf("without the webhook secret: %v", err)
	}
	if cfg.Email.ReceivesEvents() {
		t.Error("without the secret the webhook is on")
	}
	for _, raw := range []string{webhookSecret, strings.TrimPrefix(webhookSecret, "whsec_"), " " + webhookSecret + " "} {
		cfg, err := loadWith(t, merge(fullEmail(), map[string]string{"RESEND_WEBHOOK_SECRET": raw}))
		if err != nil {
			t.Fatalf("%q: %v", raw, err)
		}
		if !cfg.Email.ReceivesEvents() || strings.TrimSpace(cfg.Email.WebhookSecret.Reveal()) != strings.TrimSpace(raw) {
			t.Errorf("%q: the secret is lost: %v", raw, cfg.Email.ReceivesEvents())
		}
		if printed := fmt.Sprintf("%v %+v %#v", cfg.Email.WebhookSecret, cfg.Email, cfg.Email); strings.Contains(printed, "MDEyMzQ1") {
			t.Errorf("the secret is printed: %s", printed)
		}
	}
	for name, raw := range map[string]string{
		"not base64":           "whsec_not base64!",
		"too short":            "whsec_" + base64.StdEncoding.EncodeToString([]byte("short")),
		"the API key's form":   "re_secret_value",
		"url-safe base64 only": "whsec_" + base64.URLEncoding.EncodeToString([]byte{0xfb, 0xff, 0xfe, 0xfd, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14}),
	} {
		_, err := loadWith(t, merge(fullEmail(), map[string]string{"RESEND_WEBHOOK_SECRET": raw}))
		if err == nil {
			t.Errorf("%s was accepted", name)
			continue
		}
		if strings.Contains(err.Error(), strings.TrimPrefix(raw, "whsec_")) || !strings.Contains(err.Error(), "RESEND_WEBHOOK_SECRET") {
			t.Errorf("%s: the error repeats the secret or does not name it: %v", name, err)
		}
	}
}
