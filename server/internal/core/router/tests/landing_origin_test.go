package router_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"quizzivy/internal/core/router"
	availabilityapp "quizzivy/internal/modules/availability/application"
	availabilityhttp "quizzivy/internal/modules/availability/http"
)

const landingOrigin = "https://quizzivy.com"

func landingRouter(t *testing.T, origin string, window *maintenanceWindow) http.Handler {
	t.Helper()
	deps := router.Deps{Principals: newFakePrincipals(), DB: fakeDB{}}
	if origin != "" {
		deps.LandingOrigins = strings.Split(origin, ",")
	}
	if window != nil {
		deps.Maintenance = window
		deps.Modules = router.Modules{Availability: availabilityhttp.NewAvailability(availabilityapp.New(window, nil))}
	}
	h, err := router.New(deps, slog.New(slog.NewTextHandler(io.Discard, nil)), []string{allowedOrigin}, "")
	if err != nil {
		t.Fatalf("router.New: %v", err)
	}
	return h
}

func fromOrigin(h http.Handler, origin, method, path, address string, preflight bool) *httptest.ResponseRecorder {
	var body io.Reader
	if method == http.MethodPost && !preflight {
		body = strings.NewReader(`{}`)
	}
	target := method
	if preflight {
		target = http.MethodOptions
	}
	req := httptest.NewRequest(target, path, body)
	req.RemoteAddr = address
	req.Header.Set("Origin", origin)
	if preflight {
		req.Header.Set("Access-Control-Request-Method", method)
		req.Header.Set("Access-Control-Request-Headers", "content-type")
	} else if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func allowsCredentials(rec *httptest.ResponseRecorder) bool {
	_, set := rec.Header()["Access-Control-Allow-Credentials"]
	return set
}

// TestTheLandingOriginReachesTheFormAndTheStatusAndNothingElse fails when the
// router stops handing LANDING_URL to the CORS middleware, admits it to an
// operation outside router.LandingOperations (a sign-in, the organization, an
// Admin path), admits a near-miss of it, or answers it with credentials. The
// middleware's own tests use a handler that serves every path; this one runs
// the assembled router and its real routes.
func TestTheLandingOriginReachesTheFormAndTheStatusAndNothingElse(t *testing.T) {
	h := landingRouter(t, landingOrigin, nil)

	status := fromOrigin(h, landingOrigin, http.MethodGet, "/public/status", "203.0.113.50:1", false)
	if status.Header().Get("Access-Control-Allow-Origin") != landingOrigin || allowsCredentials(status) {
		t.Errorf("GET /public/status from the landing origin: Allow-Origin %q, credentials %v",
			status.Header().Get("Access-Control-Allow-Origin"), allowsCredentials(status))
	}
	for path, method := range map[string]string{"/public/leads": http.MethodPost, "/public/status": http.MethodGet} {
		pre := fromOrigin(h, landingOrigin, method, path, "203.0.113.50:1", true)
		if pre.Code != http.StatusNoContent || pre.Header().Get("Access-Control-Allow-Origin") != landingOrigin ||
			pre.Header().Get("Access-Control-Allow-Methods") != method || pre.Header().Get("Access-Control-Allow-Headers") != "Content-Type" || allowsCredentials(pre) {
			t.Errorf("the preflight of %s %s: %d %v", method, path, pre.Code, pre.Header())
		}
	}
	form := fromOrigin(h, landingOrigin, http.MethodPost, "/public/leads", "203.0.113.50:1", false)
	if form.Code != http.StatusBadRequest || form.Header().Get("Access-Control-Allow-Origin") != landingOrigin || allowsCredentials(form) {
		t.Errorf("POST /public/leads with an empty form: %d, Allow-Origin %q; want the 400 readable by the landing page",
			form.Code, form.Header().Get("Access-Control-Allow-Origin"))
	}

	for _, c := range []struct{ method, path string }{
		{http.MethodPost, "/auth/login"},
		{http.MethodGet, "/public/organization"},
		{http.MethodGet, "/admin/leads"},
		{http.MethodGet, "/admin/summary"},
		{http.MethodPost, "/auth/refresh"},
	} {
		for _, preflight := range []bool{false, true} {
			rec := fromOrigin(h, landingOrigin, c.method, c.path, "203.0.113.50:1", preflight)
			if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
				t.Errorf("%s %s (preflight %v) from the landing origin: Allow-Origin %q, want none", c.method, c.path, preflight, got)
			}
		}
	}
	for _, origin := range []string{"https://www.quizzivy.com", "http://quizzivy.com"} {
		for _, preflight := range []bool{false, true} {
			for path, method := range map[string]string{"/public/leads": http.MethodPost, "/public/status": http.MethodGet} {
				rec := fromOrigin(h, origin, method, path, "203.0.113.51:1", preflight)
				if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
					t.Errorf("%s %s (preflight %v) from %s: Allow-Origin %q, want none", method, path, preflight, origin, got)
				}
			}
		}
	}
	web := fromOrigin(h, allowedOrigin, http.MethodGet, "/public/status", "203.0.113.52:1", false)
	if web.Header().Get("Access-Control-Allow-Origin") != allowedOrigin || web.Header().Get("Access-Control-Allow-Credentials") != "true" {
		t.Errorf("a web host on /public/status: %v, want its credentialed answer unchanged", web.Header())
	}
}

// TestWithoutALandingOriginTheFormAndTheStatusAnswerNoOtherOrigin fails when
// an unset LANDING_URL admits the apex anyway, or any origin outside
// CORS_ALLOWED_ORIGINS.
func TestWithoutALandingOriginTheFormAndTheStatusAnswerNoOtherOrigin(t *testing.T) {
	h := landingRouter(t, "", nil)
	for _, origin := range []string{landingOrigin, "https://elsewhere.example"} {
		for _, preflight := range []bool{false, true} {
			for path, method := range map[string]string{"/public/leads": http.MethodPost, "/public/status": http.MethodGet} {
				rec := fromOrigin(h, origin, method, path, "203.0.113.53:1", preflight)
				if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
					t.Errorf("%s %s (preflight %v) from %s with no landing origin set: Allow-Origin %q", method, path, preflight, origin, got)
				}
			}
		}
	}
}

// TestARefusalOfTheFormStillCarriesTheLandingOriginsCORSAnswer fails when the
// CORS answer is written only on the way to the handler's success: the 429 of
// the per-address limit and the 503 of a maintenance window must be readable
// by the landing page, which reads Retry-After to say when to try again. The
// rate-limit tests send no Origin.
func TestARefusalOfTheFormStillCarriesTheLandingOriginsCORSAnswer(t *testing.T) {
	h := landingRouter(t, landingOrigin, nil)
	var limited *httptest.ResponseRecorder
	for i := 1; i <= 4; i++ {
		limited = fromOrigin(h, landingOrigin, http.MethodPost, "/public/leads", "203.0.113.54:1", false)
		if i <= 3 && limited.Code == http.StatusTooManyRequests {
			t.Fatalf("request %d was limited; 3 a minute are allowed", i)
		}
	}
	if limited.Code != http.StatusTooManyRequests || limited.Header().Get("Retry-After") == "" {
		t.Fatalf("the fourth request in a minute: %d with Retry-After %q, want 429", limited.Code, limited.Header().Get("Retry-After"))
	}
	if limited.Header().Get("Access-Control-Allow-Origin") != landingOrigin || allowsCredentials(limited) {
		t.Errorf("the 429: Allow-Origin %q, credentials %v", limited.Header().Get("Access-Control-Allow-Origin"), allowsCredentials(limited))
	}
	exposed := limited.Header().Get("Access-Control-Expose-Headers")
	if !strings.Contains(exposed, "Retry-After") || !strings.Contains(exposed, "X-Request-Id") {
		t.Errorf("the 429 exposes %q, want Retry-After and X-Request-Id", exposed)
	}

	gated := landingRouter(t, landingOrigin, underWay())
	closed := fromOrigin(gated, landingOrigin, http.MethodPost, "/public/leads", "203.0.113.55:1", false)
	if closed.Code != http.StatusServiceUnavailable || closed.Header().Get("Access-Control-Allow-Origin") != landingOrigin ||
		!strings.Contains(closed.Header().Get("Access-Control-Expose-Headers"), "Retry-After") || allowsCredentials(closed) {
		t.Errorf("the form during maintenance: %d %v, want 503 readable by the landing page", closed.Code, closed.Header())
	}
	open := fromOrigin(gated, landingOrigin, http.MethodGet, "/public/status", "203.0.113.55:1", false)
	if open.Code != http.StatusOK || open.Header().Get("Access-Control-Allow-Origin") != landingOrigin {
		t.Errorf("the status during maintenance: %d, Allow-Origin %q; the status page must still read it", open.Code, open.Header().Get("Access-Control-Allow-Origin"))
	}
	pre := fromOrigin(gated, landingOrigin, http.MethodPost, "/public/leads", "203.0.113.55:1", true)
	if pre.Code != http.StatusNoContent || pre.Header().Get("Access-Control-Allow-Origin") != landingOrigin {
		t.Errorf("the form's preflight during maintenance: %d, Allow-Origin %q", pre.Code, pre.Header().Get("Access-Control-Allow-Origin"))
	}
}

// TestTheLandingOperationsAreTheFormAndTheStatus fails when an operation is
// added to what the landing origin may call: the list is short on purpose, as
// theOpenTwelve is, and every entry must be an open operation.
func TestTheLandingOperationsAreTheFormAndTheStatus(t *testing.T) {
	got := router.LandingOperations()
	if !slices.Equal(got, []string{"POST /public/leads", "GET /public/status"}) {
		t.Errorf("landing operations = %v, want the form and the status", got)
	}
	for _, operation := range got {
		if !slices.Contains(theOpenTwelve, operation) {
			t.Errorf("%s is admitted from the landing origin and is not an open operation", operation)
		}
	}
}

// TestEachLandingOriginOfAListReachesTheFormAndTheStatusAndNoOtherOriginDoes
// fails when the router hands only the first origin of LANDING_URL to the
// CORS middleware, answers one listed origin with the other's name, admits a
// listed origin to a sign-in, or admits an origin the list does not hold
// because it resembles one that it does. The tests above configure a single
// origin.
func TestEachLandingOriginOfAListReachesTheFormAndTheStatusAndNoOtherOriginDoes(t *testing.T) {
	const pagesOrigin = "https://quizzivy-landing.pages.dev"
	h := landingRouter(t, landingOrigin+","+pagesOrigin, nil)
	operations := map[string]string{"/public/leads": http.MethodPost, "/public/status": http.MethodGet}

	for i, origin := range []string{landingOrigin, pagesOrigin} {
		address := "203.0.113.6" + string(rune('0'+i)) + ":1"
		for path, method := range operations {
			simple := fromOrigin(h, origin, method, path, address, false)
			exposed := simple.Header().Get("Access-Control-Expose-Headers")
			if simple.Header().Get("Access-Control-Allow-Origin") != origin || allowsCredentials(simple) ||
				!strings.Contains(exposed, "Retry-After") || !strings.Contains(exposed, "X-Request-Id") {
				t.Errorf("%s %s from %s: Allow-Origin %q, credentials %v, exposed %q; want that origin, no credentials, Retry-After and X-Request-Id",
					method, path, origin, simple.Header().Get("Access-Control-Allow-Origin"), allowsCredentials(simple), exposed)
			}
			pre := fromOrigin(h, origin, method, path, address, true)
			if pre.Code != http.StatusNoContent || pre.Header().Get("Access-Control-Allow-Origin") != origin ||
				pre.Header().Get("Access-Control-Allow-Methods") != method || pre.Header().Get("Access-Control-Allow-Headers") != "Content-Type" || allowsCredentials(pre) {
				t.Errorf("the preflight of %s %s from %s: %d %v", method, path, origin, pre.Code, pre.Header())
			}
		}
		for _, preflight := range []bool{false, true} {
			login := fromOrigin(h, origin, http.MethodPost, "/auth/login", address, preflight)
			if got := login.Header().Get("Access-Control-Allow-Origin"); got != "" {
				t.Errorf("POST /auth/login (preflight %v) from %s: Allow-Origin %q, want none", preflight, origin, got)
			}
		}
	}

	for _, stranger := range []string{"https://quizzivy-landing-preview.pages.dev", "https://pages.dev", "https://staging.quizzivy.com", landingOrigin + "," + pagesOrigin} {
		for path, method := range operations {
			for _, preflight := range []bool{false, true} {
				rec := fromOrigin(h, stranger, method, path, "203.0.113.69:1", preflight)
				if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
					t.Errorf("%s %s (preflight %v) from %s, which the list does not hold: Allow-Origin %q", method, path, preflight, stranger, got)
				}
			}
		}
	}
}
