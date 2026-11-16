package config

import (
	"fmt"
	"net/mail"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"
	"unicode"

	"quizzivy/internal/platform/email"
	"quizzivy/internal/platform/secrets"
)

// Email is the outgoing mail's configuration. RESEND_API_KEY and EMAIL_FROM
// are one all-or-nothing group, required in production. Without the group
// outside production the messages go to the sink directory SinkDir names
// instead. A link in a message points into Config.Hosts.
//
// Events are the notification emails NOTIFY_EMAIL_EVENTS leaves on, by the
// names email.NotifyEvents lists: every one when it is unset or empty, none
// for "none", and otherwise those its comma list names. A Config built
// without Load has nil Events, which sends every one, as unset does.
//
// WebhookSecret is RESEND_WEBHOOK_SECRET, the signing secret of Resend's
// webhook (T-R7.15), checked at start-up with email.ParseWebhookSecret. It is
// optional: without it POST /public/email-events answers 501 and no bounce or
// complaint reaches the suppression list.
type Email struct {
	APIKey        Secret
	From          string
	ReplyTo       string
	DailyLimit    int
	SendRate      int
	SinkDir       string
	Events        []string
	WebhookSecret Secret
}

// ReceivesEvents reports whether RESEND_WEBHOOK_SECRET is set, so Resend's
// bounces and complaints are received.
func (e Email) ReceivesEvents() bool { return e.WebhookSecret != "" }

// Sends reports whether NOTIFY_EMAIL_EVENTS leaves the notification email of
// event on.
func (e Email) Sends(event string) bool { return slices.Contains(e.NotifyEvents(), event) }

// NotifyEvents lists the notification emails that are sent: Events, or every
// one while Events is nil.
func (e Email) NotifyEvents() []string {
	if e.Events == nil {
		return email.NotifyEvents()
	}
	return e.Events
}

// Enabled reports whether the Resend group is configured.
func (e Email) Enabled() bool { return e.APIKey != "" }

// SendInterval is the pause between two sends at SendRate a second.
func (e Email) SendInterval() time.Duration {
	if e.SendRate < 1 {
		return 0
	}
	return time.Second / time.Duration(e.SendRate)
}

const (
	defaultEmailDailyLimit = 100
	maxEmailDailyLimit     = 100000
	defaultEmailSendRate   = 2
	maxEmailSendRate       = 100
	defaultEmailSinkDir    = "tmp/email-sink"
)

func loadEmail(cfg *Config) error {
	if err := loadResend(cfg); err != nil {
		return err
	}
	if err := loadWebhookSecret(cfg); err != nil {
		return err
	}
	return loadEmailLimits(cfg)
}

func loadWebhookSecret(cfg *Config) error {
	raw := strings.TrimSpace(os.Getenv("RESEND_WEBHOOK_SECRET"))
	if raw == "" {
		return nil
	}
	if _, err := email.ParseWebhookSecret(raw); err != nil {
		return err
	}
	cfg.Email.WebhookSecret = Secret(raw)
	return nil
}

func loadResend(cfg *Config) error {
	e := &cfg.Email
	key := strings.TrimSpace(os.Getenv("RESEND_API_KEY"))
	from := strings.TrimSpace(os.Getenv("EMAIL_FROM"))
	e.ReplyTo = strings.TrimSpace(os.Getenv("EMAIL_REPLY_TO"))
	e.SinkDir = strings.TrimSpace(os.Getenv("EMAIL_SINK_DIR"))
	production := cfg.Env == productionEnv
	set := 0
	for _, v := range []string{key, from} {
		if v != "" {
			set++
		}
	}
	if err := checkEmailGroup(set, production, e.SinkDir); err != nil {
		return err
	}
	if e.SinkDir == "" {
		e.SinkDir = defaultEmailSinkDir
	}
	if e.ReplyTo != "" {
		if err := checkMailbox("EMAIL_REPLY_TO", e.ReplyTo); err != nil {
			return err
		}
	}
	if set == 0 {
		return nil
	}
	if strings.ContainsAny(key, " \t\r\n") {
		return fmt.Errorf("RESEND_API_KEY must be a single token with no whitespace; re-copy it from the provider console")
	}
	if err := checkMailbox("EMAIL_FROM", from); err != nil {
		return err
	}
	e.APIKey, e.From = Secret(key), from
	return nil
}

func checkEmailGroup(set int, production bool, sinkDir string) error {
	switch {
	case set != 0 && set != 2:
		return fmt.Errorf("email needs RESEND_API_KEY and EMAIL_FROM together, or neither")
	case set == 0 && production:
		return fmt.Errorf("RESEND_API_KEY and EMAIL_FROM are required in production: without them the sink would take every message")
	case sinkDir != "" && production:
		return fmt.Errorf("EMAIL_SINK_DIR must not be set in production: it writes every message, links included, to disk instead of sending it")
	}
	return nil
}

func checkMailbox(name, raw string) error {
	if strings.ContainsFunc(raw, unicode.IsControl) {
		return fmt.Errorf("%s must not contain a line break or other control character", name)
	}
	if _, err := mail.ParseAddress(raw); err != nil {
		return fmt.Errorf("%s must be one email address, such as Quizzivy <no-reply@quizzivy.com>", name)
	}
	return nil
}

func parseAppOrigin(name, raw string, production bool) (string, error) {
	origin, err := parseOrigin(name, raw, "https://my.quizzivy.com")
	if err != nil {
		return "", err
	}
	if production && !strings.HasPrefix(origin, schemeHTTPS+"://") {
		return "", fmt.Errorf("%s must be https in production: a set-password link travels in it", name)
	}
	return origin, nil
}

func parseOrigin(name, raw, example string) (string, error) {
	if strings.ContainsFunc(raw, func(r rune) bool { return unicode.IsControl(r) || unicode.IsSpace(r) }) {
		return "", fmt.Errorf("%s must not contain whitespace or a control character", name)
	}
	u, err := url.Parse(raw)
	switch {
	case err != nil || (u.Scheme != schemeHTTPS && u.Scheme != "http") || u.Host == "" || u.User != nil:
		return "", fmt.Errorf("%s must be an origin such as %s", name, example)
	case (u.Path != "" && u.Path != "/") || strings.ContainsAny(raw, "?#"):
		return "", fmt.Errorf("%s must be an origin such as %s, with no path, query or fragment", name, example)
	}
	return u.Scheme + "://" + strings.ToLower(u.Host), nil
}

func loadEmailLimits(cfg *Config) error {
	e := &cfg.Email
	var err error
	if e.DailyLimit, err = getenvInt("EMAIL_DAILY_LIMIT", defaultEmailDailyLimit); err != nil {
		return err
	}
	if e.DailyLimit < 1 || e.DailyLimit > maxEmailDailyLimit {
		return fmt.Errorf("EMAIL_DAILY_LIMIT must be between 1 and %d", maxEmailDailyLimit)
	}
	if e.SendRate, err = getenvInt("EMAIL_SEND_RATE", defaultEmailSendRate); err != nil {
		return err
	}
	if e.SendRate < 1 || e.SendRate > maxEmailSendRate {
		return fmt.Errorf("EMAIL_SEND_RATE must be between 1 and %d a second", maxEmailSendRate)
	}
	e.Events, err = notifyEmailEvents(os.Getenv("NOTIFY_EMAIL_EVENTS"))
	return err
}

// notifyEmailEvents reads NOTIFY_EMAIL_EVENTS: a comma list of the names
// email.NotifyEvents lists, every one when raw is empty, none for "none". A
// name it does not know is refused, so a typo cannot leave an event on.
func notifyEmailEvents(raw string) ([]string, error) {
	known := email.NotifyEvents()
	raw = strings.TrimSpace(raw)
	switch raw {
	case "":
		return known, nil
	case "none":
		return []string{}, nil
	}
	var out []string
	for _, name := range strings.Split(raw, ",") {
		name = strings.TrimSpace(name)
		switch {
		case name == "":
			continue
		case !slices.Contains(known, name):
			return nil, fmt.Errorf("NOTIFY_EMAIL_EVENTS names %q, which is none of %s (or set it to none)", name, strings.Join(known, ", "))
		case !slices.Contains(out, name):
			out = append(out, name)
		}
	}
	return out, nil
}

func loadSecretsKey(cfg *Config) error {
	raw := strings.TrimSpace(os.Getenv("SECRETS_KEY"))
	if raw == "" {
		if cfg.Env == productionEnv {
			return fmt.Errorf("SECRETS_KEY is required in production: standard base64 of %d random bytes; generate one with: openssl rand -base64 32", secrets.KeySize)
		}
		return nil
	}
	key, err := secrets.ParseKey(raw)
	if err != nil {
		return err
	}
	cfg.SecretsKey = key
	return nil
}

func loadAppLocation(cfg *Config) error {
	var err error
	cfg.AppLocation, err = AppLocation()
	return err
}

func loadEmailGroup(cfg *Config) error {
	for _, load := range []func(*Config) error{loadSecretsKey, loadAppLocation, loadEmail} {
		if err := load(cfg); err != nil {
			return err
		}
	}
	return nil
}
