package config_test

import (
	"slices"
	"strings"
	"testing"
)

const landingHost = "https://quizzivy.com"

// TestTheLandingURLIsOptionalAndLoadsAsAnOrigin fails when a deployment
// without LANDING_URL stops starting or admits an origin anyway, and when the
// value is kept as written rather than as the origin a browser sends: scheme,
// lower-case host and port, no trailing slash.
func TestTheLandingURLIsOptionalAndLoadsAsAnOrigin(t *testing.T) {
	cfg, err := loadWith(t, nil)
	if err != nil || cfg.LandingOrigin != "" {
		t.Fatalf("without LANDING_URL: origin %q, %v; want none and a server that starts", cfg.LandingOrigin, err)
	}
	for raw, want := range map[string]string{
		landingHost:                 landingHost,
		"HTTPS://Quizzivy.COM/":     landingHost,
		"https://quizzivy.com:8443": "https://quizzivy.com:8443",
		"http://localhost:4321":     "http://localhost:4321",
	} {
		cfg, err := loadWith(t, map[string]string{"LANDING_URL": raw})
		if err != nil || cfg.LandingOrigin != want {
			t.Errorf("LANDING_URL=%q: origin %q, %v; want %q", raw, cfg.LandingOrigin, err, want)
		}
	}
	cfg, err = loadWith(t, merge(fullEmail(), merge(productionHosts(), map[string]string{"LANDING_URL": landingHost})))
	if err != nil || cfg.LandingOrigin != landingHost {
		t.Errorf("production with the apex: origin %q, %v", cfg.LandingOrigin, err)
	}
}

// TestALandingURLThatIsNoOriginOrIsAWebHostIsRefusedByName fails when startup
// accepts a LANDING_URL with a path, a query or no scheme, one over http in
// production, or one that is also a web host: STUDENT_APP_URL, STAFF_APP_URL
// or any origin of CORS_ALLOWED_ORIGINS, whose pages call every operation with
// credentials. The two web hosts have their own refusals test.
func TestALandingURLThatIsNoOriginOrIsAWebHostIsRefusedByName(t *testing.T) {
	hosts := map[string]string{"STUDENT_APP_URL": studentHost, "STAFF_APP_URL": staffHost, "CORS_ALLOWED_ORIGINS": studentHost + "," + staffHost}
	for name, tc := range map[string]struct {
		env  map[string]string
		want string
	}{
		"a path":                     {map[string]string{"LANDING_URL": landingHost + "/vi"}, "LANDING_URL must be an origin such as https://quizzivy.com"},
		"a query":                    {map[string]string{"LANDING_URL": landingHost + "?utm=1"}, "LANDING_URL must be an origin"},
		"a fragment":                 {map[string]string{"LANDING_URL": landingHost + "#form"}, "LANDING_URL must be an origin"},
		"no scheme":                  {map[string]string{"LANDING_URL": "quizzivy.com"}, "LANDING_URL must be an origin"},
		"another scheme":             {map[string]string{"LANDING_URL": "ftp://quizzivy.com"}, "LANDING_URL must be an origin"},
		"a user":                     {map[string]string{"LANDING_URL": "https://u@quizzivy.com"}, "LANDING_URL must be an origin"},
		"a space":                    {map[string]string{"LANDING_URL": "https://quizzivy .com"}, "LANDING_URL must not contain"},
		"a wildcard":                 {map[string]string{"LANDING_URL": "*"}, "LANDING_URL must be an origin"},
		"two origins":                {map[string]string{"LANDING_URL": landingHost + ",https://www.quizzivy.com"}, "LANDING_URL must be an origin"},
		"http in production":         {merge(fullEmail(), merge(productionHosts(), map[string]string{"LANDING_URL": "http://quizzivy.com"})), "LANDING_URL must be https in production"},
		"the student host":           {merge(hosts, map[string]string{"LANDING_URL": studentHost}), "LANDING_URL must not be STUDENT_APP_URL"},
		"the staff host":             {merge(hosts, map[string]string{"LANDING_URL": "https://PORTAL.quizzivy.com/"}), "LANDING_URL must not be STAFF_APP_URL"},
		"the dev server, both hosts": {map[string]string{"LANDING_URL": "http://localhost:5173"}, "LANDING_URL must not be STUDENT_APP_URL"},
		"an origin CORS lists":       {merge(hosts, map[string]string{"LANDING_URL": landingHost, "CORS_ALLOWED_ORIGINS": studentHost + "," + staffHost + ", " + landingHost}), "LANDING_URL must not be listed in CORS_ALLOWED_ORIGINS"},
	} {
		cfg, err := loadWith(t, tc.env)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v (origin %q), want an error naming %q", name, err, cfg.LandingOrigin, tc.want)
		}
	}
}

// TestEnvExampleListsTheLandingURLWithNoValue fails when .env.example stops
// naming the setting, or ships a value that would admit an origin in a
// developer's copy.
func TestEnvExampleListsTheLandingURLWithNoValue(t *testing.T) {
	if lines := strings.Split(repoFile(t, ".env.example"), "\n"); !slices.Contains(lines, "LANDING_URL=") {
		t.Error(".env.example does not list LANDING_URL with an empty value")
	}
}
