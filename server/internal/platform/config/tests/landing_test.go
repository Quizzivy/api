package config_test

import (
	"slices"
	"strings"
	"testing"

	"quizzivy/internal/platform/config"
)

const (
	landingHost = "https://quizzivy.com"
	pagesHost   = "https://quizzivy-landing.pages.dev"
)

func landingOf(cfg config.Config) string { return strings.Join(cfg.LandingOrigins, ",") }

// TestTheLandingURLIsOptionalAndLoadsAsAnOrigin fails when a deployment
// without LANDING_URL stops starting or admits an origin anyway, and when the
// value is kept as written rather than as the origin a browser sends: scheme,
// lower-case host and port, no trailing slash.
func TestTheLandingURLIsOptionalAndLoadsAsAnOrigin(t *testing.T) {
	cfg, err := loadWith(t, nil)
	if err != nil || landingOf(cfg) != "" {
		t.Fatalf("without LANDING_URL: origin %q, %v; want none and a server that starts", landingOf(cfg), err)
	}
	for raw, want := range map[string]string{
		landingHost:                 landingHost,
		"HTTPS://Quizzivy.COM/":     landingHost,
		"https://quizzivy.com:8443": "https://quizzivy.com:8443",
		"http://localhost:4321":     "http://localhost:4321",
	} {
		cfg, err := loadWith(t, map[string]string{"LANDING_URL": raw})
		if err != nil || landingOf(cfg) != want {
			t.Errorf("LANDING_URL=%q: origin %q, %v; want %q", raw, landingOf(cfg), err, want)
		}
	}
	cfg, err = loadWith(t, merge(fullEmail(), merge(productionHosts(), map[string]string{"LANDING_URL": landingHost})))
	if err != nil || landingOf(cfg) != landingHost {
		t.Errorf("production with the apex: origin %q, %v", landingOf(cfg), err)
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
		"http in production":         {merge(fullEmail(), merge(productionHosts(), map[string]string{"LANDING_URL": "http://quizzivy.com"})), "LANDING_URL must be https in production"},
		"the student host":           {merge(hosts, map[string]string{"LANDING_URL": studentHost}), "LANDING_URL must not be STUDENT_APP_URL"},
		"the staff host":             {merge(hosts, map[string]string{"LANDING_URL": "https://PORTAL.quizzivy.com/"}), "LANDING_URL must not be STAFF_APP_URL"},
		"the dev server, both hosts": {map[string]string{"LANDING_URL": "http://localhost:5173"}, "LANDING_URL must not be STUDENT_APP_URL"},
		"an origin CORS lists":       {merge(hosts, map[string]string{"LANDING_URL": landingHost, "CORS_ALLOWED_ORIGINS": studentHost + "," + staffHost + ", " + landingHost}), "LANDING_URL must not be listed in CORS_ALLOWED_ORIGINS"},
	} {
		cfg, err := loadWith(t, tc.env)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v (origin %q), want an error naming %q", name, err, landingOf(cfg), tc.want)
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

// TestALandingURLOfSeveralOriginsLoadsEachInTheOrderWritten fails when
// LANDING_URL takes one origin only (a second, for trying the form on the
// Pages project's own host, is then refused or dropped), when an item is kept
// as written rather than as the origin a browser sends, or when the list is
// reordered. The single-origin test above never writes a comma.
func TestALandingURLOfSeveralOriginsLoadsEachInTheOrderWritten(t *testing.T) {
	for raw, want := range map[string][]string{
		landingHost + "," + pagesHost:                                {landingHost, pagesHost},
		pagesHost + " , " + landingHost:                              {pagesHost, landingHost},
		" HTTPS://Quizzivy.com/ ,https://Quizzivy-Landing.PAGES.dev": {landingHost, pagesHost},
		landingHost + "," + pagesHost + ",http://localhost:4321":     {landingHost, pagesHost, "http://localhost:4321"},
	} {
		cfg, err := loadWith(t, map[string]string{"LANDING_URL": raw})
		if err != nil || !slices.Equal(cfg.LandingOrigins, want) {
			t.Errorf("LANDING_URL=%q: origins %q, %v; want %q", raw, cfg.LandingOrigins, err, want)
		}
	}
	cfg, err := loadWith(t, merge(fullEmail(), merge(productionHosts(), map[string]string{"LANDING_URL": landingHost + "," + pagesHost})))
	if err != nil || !slices.Equal(cfg.LandingOrigins, []string{landingHost, pagesHost}) {
		t.Errorf("production with the apex and the Pages host: origins %q, %v", cfg.LandingOrigins, err)
	}
	for _, blank := range []string{"", "   ", "\t"} {
		cfg, err := loadWith(t, map[string]string{"LANDING_URL": blank})
		if err != nil || len(cfg.LandingOrigins) != 0 {
			t.Errorf("LANDING_URL=%q: origins %q, %v; want none and a server that starts", blank, cfg.LandingOrigins, err)
		}
	}
}

// TestEveryRuleOfALandingOriginHoldsForEachItemOfAList fails when a rule is
// checked on the first item of LANDING_URL only, so that a web host, an http
// origin in production or a path slips in as the second; when an origin named
// twice or an empty item (a doubled or trailing comma) is accepted, which
// would hide a typo behind a list that admits less than was meant; and when
// the refusal does not say which item it is about. The single-origin
// refusals above hold one item each.
func TestEveryRuleOfALandingOriginHoldsForEachItemOfAList(t *testing.T) {
	hosts := map[string]string{"STUDENT_APP_URL": studentHost, "STAFF_APP_URL": staffHost, "CORS_ALLOWED_ORIGINS": studentHost + "," + staffHost}
	second := func(item string) map[string]string { return map[string]string{"LANDING_URL": landingHost + "," + item} }
	for name, tc := range map[string]struct {
		env  map[string]string
		want string
	}{
		"a path second":             {second(pagesHost + "/vi"), "LANDING_URL (origin 2) must be an origin such as https://quizzivy.com"},
		"a query second":            {second(pagesHost + "?utm=1"), "LANDING_URL (origin 2) must be an origin"},
		"no scheme second":          {second("quizzivy-landing.pages.dev"), "LANDING_URL (origin 2) must be an origin"},
		"a wildcard second":         {second("*"), "LANDING_URL (origin 2) must be an origin"},
		"a space inside the second": {second("https://quizzivy landing.pages.dev"), "LANDING_URL (origin 2) must not contain"},
		"http second in production": {merge(fullEmail(), merge(productionHosts(), second("http://quizzivy-landing.pages.dev"))), "LANDING_URL (origin 2) must be https in production"},
		"the student host second":   {merge(hosts, second(studentHost)), "LANDING_URL (origin 2) must not be STUDENT_APP_URL"},
		"the staff host second":     {merge(hosts, second("https://PORTAL.quizzivy.com/")), "LANDING_URL (origin 2) must not be STAFF_APP_URL"},
		"the dev server second":     {second("http://localhost:5173"), "LANDING_URL (origin 2) must not be STUDENT_APP_URL"},
		"an origin CORS lists second": {merge(hosts, merge(second(pagesHost), map[string]string{"CORS_ALLOWED_ORIGINS": studentHost + "," + staffHost + "," + pagesHost})),
			"LANDING_URL (origin 2) must not be listed in CORS_ALLOWED_ORIGINS"},
		"a web host third":           {merge(hosts, map[string]string{"LANDING_URL": landingHost + "," + pagesHost + "," + staffHost}), "LANDING_URL (origin 3) must not be STAFF_APP_URL"},
		"a web host first of two":    {merge(hosts, map[string]string{"LANDING_URL": studentHost + "," + landingHost}), "LANDING_URL (origin 1) must not be STUDENT_APP_URL"},
		"the same origin twice":      {second(landingHost), "LANDING_URL (origin 2) repeats an origin"},
		"the same origin respelled":  {second("HTTPS://Quizzivy.com/"), "LANDING_URL (origin 2) repeats an origin"},
		"a repeat as the third":      {map[string]string{"LANDING_URL": landingHost + "," + pagesHost + "," + landingHost}, "LANDING_URL (origin 3) repeats an origin"},
		"a doubled comma":            {map[string]string{"LANDING_URL": landingHost + ",," + pagesHost}, "LANDING_URL (origin 2) is empty"},
		"a trailing comma":           {map[string]string{"LANDING_URL": landingHost + ","}, "LANDING_URL (origin 2) is empty"},
		"a trailing comma and space": {map[string]string{"LANDING_URL": landingHost + ", "}, "LANDING_URL (origin 2) is empty"},
		"a leading comma":            {map[string]string{"LANDING_URL": "," + landingHost}, "LANDING_URL (origin 1) is empty"},
		"a comma alone":              {map[string]string{"LANDING_URL": ","}, "LANDING_URL (origin 1) is empty"},
	} {
		_, err := loadWith(t, tc.env)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want an error naming %q", name, err, tc.want)
		}
	}
}
