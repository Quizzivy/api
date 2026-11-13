package config_test

import (
	"strings"
	"testing"

	"quizzivy/internal/platform/config"
	"quizzivy/internal/shared/access"
)

const (
	studentHost = "https://my.quizzivy.com"
	staffHost   = "https://portal.quizzivy.com"
)

var hostVariables = []string{"STUDENT_APP_URL", "STAFF_APP_URL", "APP_URL"}

func productionHosts() map[string]string {
	return map[string]string{
		"APP_ENV": "production", "STUDENT_APP_URL": studentHost, "STAFF_APP_URL": staffHost,
		"CORS_ALLOWED_ORIGINS": studentHost + "," + staffHost,
	}
}

func TestWithoutTheHostSettingsOutsideProductionBothHostsAreTheDevServer(t *testing.T) {
	cfg, err := loadWith(t, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Hosts != (access.Hosts{Student: config.DevAppURL, Staff: config.DevAppURL}) {
		t.Fatalf("hosts: %+v", cfg.Hosts)
	}
	if got := cfg.Hosts.AudienceOf(config.DevAppURL); got != access.AudienceAny {
		t.Errorf("the dev server's audience is %q, want one host for every account", got)
	}
}

func TestTheTwoHostSettingsLoadAsOriginsAndDecideARequestsAudience(t *testing.T) {
	cfg, err := loadWith(t, map[string]string{
		"STUDENT_APP_URL": "HTTPS://MY.Quizzivy.com:8443/", "STAFF_APP_URL": staffHost,
		"CORS_ALLOWED_ORIGINS": "https://my.quizzivy.com:8443, " + staffHost,
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Hosts != (access.Hosts{Student: "https://my.quizzivy.com:8443", Staff: staffHost}) {
		t.Fatalf("hosts: %+v", cfg.Hosts)
	}
	for origin, want := range map[string]access.Audience{
		"https://my.quizzivy.com:8443": access.AudienceStudent,
		staffHost:                      access.AudienceStaff,
		"https://my.quizzivy.com":      access.AudienceAny,
		"https://app.quizzivy.com":     access.AudienceAny,
		"":                             access.AudienceAny,
	} {
		if got := cfg.Hosts.AudienceOf(origin); got != want {
			t.Errorf("the audience of %q is %q, want %q", origin, got, want)
		}
	}
}

func TestALeftoverAppURLIsIgnored(t *testing.T) {
	cfg, err := loadWith(t, merge(fullEmail(), merge(productionHosts(), map[string]string{"APP_URL": "https://app.quizzivy.com"})))
	if err != nil {
		t.Fatalf("a deployment that still sets APP_URL does not start: %v", err)
	}
	if cfg.Hosts.Student != studentHost || cfg.Hosts.Staff != staffHost {
		t.Fatalf("hosts: %+v", cfg.Hosts)
	}
}

func TestTheHostSettingsAreRefusedByName(t *testing.T) {
	for name, tc := range map[string]struct {
		env  map[string]string
		want string
	}{
		"only the student host":         {map[string]string{"STUDENT_APP_URL": studentHost, "CORS_ALLOWED_ORIGINS": studentHost}, "set together"},
		"only the staff host":           {map[string]string{"STAFF_APP_URL": staffHost, "CORS_ALLOWED_ORIGINS": staffHost}, "set together"},
		"production without them":       {merge(fullEmail(), map[string]string{"APP_ENV": "production"}), "STUDENT_APP_URL and STAFF_APP_URL are required in production"},
		"production over http":          {merge(fullEmail(), merge(productionHosts(), map[string]string{"STAFF_APP_URL": "http://portal.quizzivy.com", "CORS_ALLOWED_ORIGINS": studentHost + ",http://portal.quizzivy.com"})), "STAFF_APP_URL must be https in production"},
		"a host CORS does not list":     {map[string]string{"STUDENT_APP_URL": studentHost, "STAFF_APP_URL": staffHost, "CORS_ALLOWED_ORIGINS": studentHost}, "STAFF_APP_URL must be listed in CORS_ALLOWED_ORIGINS"},
		"a host with a path":            {map[string]string{"STUDENT_APP_URL": studentHost + "/app", "STAFF_APP_URL": staffHost}, "STUDENT_APP_URL must be an origin"},
		"a host that is no url":         {map[string]string{"STUDENT_APP_URL": "my.quizzivy.com", "STAFF_APP_URL": staffHost}, "STUDENT_APP_URL must be an origin"},
		"a host ending in ?":            {map[string]string{"STUDENT_APP_URL": studentHost + "?", "STAFF_APP_URL": staffHost}, "STUDENT_APP_URL must be an origin"},
		"a host ending in #":            {map[string]string{"STUDENT_APP_URL": studentHost, "STAFF_APP_URL": staffHost + "#"}, "STAFF_APP_URL must be an origin"},
		"a host with a query":           {map[string]string{"STUDENT_APP_URL": studentHost, "STAFF_APP_URL": staffHost + "?a=1"}, "STAFF_APP_URL must be an origin"},
		"a host with a line break":      {map[string]string{"STUDENT_APP_URL": studentHost + "\r\nBcc: x@y.com", "STAFF_APP_URL": staffHost}, "STUDENT_APP_URL must not contain"},
		"a host with a space":           {map[string]string{"STUDENT_APP_URL": "https://my.quizzivy .com", "STAFF_APP_URL": staffHost}, "STUDENT_APP_URL must not contain"},
		"a host with a user and a pass": {map[string]string{"STUDENT_APP_URL": "https://u:p@my.quizzivy.com", "STAFF_APP_URL": staffHost}, "STUDENT_APP_URL must be an origin"},
	} {
		_, err := loadWith(t, tc.env)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want an error naming %q", name, err, tc.want)
		}
	}
}
