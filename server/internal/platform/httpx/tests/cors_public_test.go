package httpx_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"quizzivy/internal/platform/httpx"
)

const landing = "https://quizzivy.com"

var landingOperations = []string{"POST /public/leads", "GET /public/status"}

func landingHandler() http.Handler {
	return httpx.CORS([]string{allowed}, httpx.PublicOrigin{Origin: landing, Operations: landingOperations})(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) }))
}

func from(h http.Handler, origin, method, path, preflightOf string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	if preflightOf != "" {
		req.Header.Set("Access-Control-Request-Method", preflightOf)
		req.Header.Set("Access-Control-Request-Headers", "content-type")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func corsHeaders(rec *httptest.ResponseRecorder) string {
	var found []string
	for name, values := range rec.Header() {
		if strings.HasPrefix(name, "Access-Control-") {
			found = append(found, name+": "+strings.Join(values, ","))
		}
	}
	return strings.Join(found, "; ")
}

// TestAPublicOriginReachesItsOperationsWithoutCredentials fails when a public
// origin's own operation answers no Access-Control-Allow-Origin, answers one
// with Access-Control-Allow-Credentials (a page of that origin could then act
// with a visitor's refresh cookie), or stops exposing Retry-After and
// X-Request-Id, which the consultation form reads on a 429. The tests of the
// web hosts never send an origin outside the allowlist that is admitted.
func TestAPublicOriginReachesItsOperationsWithoutCredentials(t *testing.T) {
	for _, operation := range landingOperations {
		method, path, _ := strings.Cut(operation, " ")
		rec := from(landingHandler(), landing, method, path, "")
		h := rec.Header()
		if rec.Code != http.StatusTeapot {
			t.Errorf("%s: status %d, the request did not reach the handler", operation, rec.Code)
		}
		if got := h.Get("Access-Control-Allow-Origin"); got != landing {
			t.Errorf("%s: Allow-Origin = %q, want %q", operation, got, landing)
		}
		if _, set := h["Access-Control-Allow-Credentials"]; set {
			t.Errorf("%s: Allow-Credentials is set (%q); a public origin never gets credentials", operation, h.Get("Access-Control-Allow-Credentials"))
		}
		exposed := h.Get("Access-Control-Expose-Headers")
		for _, name := range []string{"Retry-After", "X-Request-Id"} {
			if !strings.Contains(exposed, name) {
				t.Errorf("%s: Expose-Headers = %q, want it to name %s", operation, exposed, name)
			}
		}
		if !strings.Contains(strings.Join(h.Values("Vary"), ","), "Origin") {
			t.Errorf("%s: Vary = %v, want Origin", operation, h.Values("Vary"))
		}
	}
}

// TestAPublicOriginsPreflightAllowsItsOneMethodAndContentType fails when the
// preflight of a public operation is refused (the browser then never sends
// the form), allows the web hosts' full method list or the Authorization
// header, or allows credentials.
func TestAPublicOriginsPreflightAllowsItsOneMethodAndContentType(t *testing.T) {
	for _, operation := range landingOperations {
		method, path, _ := strings.Cut(operation, " ")
		rec := from(landingHandler(), landing, http.MethodOptions, path, method)
		h := rec.Header()
		if rec.Code != http.StatusNoContent {
			t.Errorf("%s: preflight status %d, want 204 without reaching the handler", operation, rec.Code)
		}
		if h.Get("Access-Control-Allow-Origin") != landing || h.Get("Access-Control-Allow-Methods") != method ||
			h.Get("Access-Control-Allow-Headers") != "Content-Type" || h.Get("Access-Control-Max-Age") == "" {
			t.Errorf("%s: preflight answered %s; want the origin, the method %s, Content-Type and a max age", operation, corsHeaders(rec), method)
		}
		if _, set := h["Access-Control-Allow-Credentials"]; set {
			t.Errorf("%s: the preflight allows credentials", operation)
		}
		vary := strings.Join(h.Values("Vary"), ",")
		for _, name := range []string{"Origin", "Access-Control-Request-Method", "Access-Control-Request-Headers"} {
			if !strings.Contains(vary, name) {
				t.Errorf("%s: Vary = %q, want %s", operation, vary, name)
			}
		}
	}
}

// TestAPublicOriginIsAnsweredAsAStrangerEverywhereElse fails when a public
// origin is admitted to a path or a method outside its operations, by a
// simple request or by a preflight: a sign-in, another open read, an Admin
// path, the form's path under another method, or a path that only begins like
// one of its own.
func TestAPublicOriginIsAnsweredAsAStrangerEverywhereElse(t *testing.T) {
	for _, c := range []struct{ method, path string }{
		{http.MethodPost, "/auth/login"},
		{http.MethodPost, "/auth/refresh"},
		{http.MethodGet, "/public/organization"},
		{http.MethodGet, "/admin/leads"},
		{http.MethodGet, "/public/leads"},
		{http.MethodPut, "/public/leads"},
		{http.MethodDelete, "/public/leads"},
		{http.MethodPost, "/public/status"},
		{http.MethodHead, "/public/status"},
		{http.MethodPost, "/public/leads/"},
		{http.MethodPost, "/public/leads/019535d9-3df7-79fb-b466-fa907fa17f9e"},
		{http.MethodGet, "/public/status/x"},
		{http.MethodGet, "/"},
	} {
		if rec := from(landingHandler(), landing, c.method, c.path, ""); corsHeaders(rec) != "" {
			t.Errorf("%s %s from the public origin answered %s", c.method, c.path, corsHeaders(rec))
		}
		rec := from(landingHandler(), landing, http.MethodOptions, c.path, c.method)
		if rec.Code != http.StatusNoContent || corsHeaders(rec) != "" {
			t.Errorf("the preflight of %s %s from the public origin answered %d %s, want 204 with no CORS header", c.method, c.path, rec.Code, corsHeaders(rec))
		}
	}
}

// TestANearMissOfAPublicOriginIsRefused fails when the public origin is
// compared by suffix, by host alone or without its scheme and port.
func TestANearMissOfAPublicOriginIsRefused(t *testing.T) {
	for _, origin := range []string{
		"https://www.quizzivy.com", "http://quizzivy.com", "https://quizzivy.com:8443", "https://quizzivy.com.evil.example",
		"https://evilquizzivy.com", "https://QUIZZIVY.com", "https://quizzivy.com/", "null", "",
	} {
		for _, operation := range landingOperations {
			method, path, _ := strings.Cut(operation, " ")
			if rec := from(landingHandler(), origin, method, path, ""); corsHeaders(rec) != "" {
				t.Errorf("%s from %q answered %s", operation, origin, corsHeaders(rec))
			}
			if rec := from(landingHandler(), origin, http.MethodOptions, path, method); corsHeaders(rec) != "" {
				t.Errorf("the preflight of %s from %q answered %s", operation, origin, corsHeaders(rec))
			}
		}
	}
}

// TestAWebHostKeepsItsCredentialedAnswerOnAPublicOperation fails when adding
// a public origin changes what a web host of the allowlist is answered on the
// same paths: its pages still call them with credentials.
func TestAWebHostKeepsItsCredentialedAnswerOnAPublicOperation(t *testing.T) {
	rec := from(landingHandler(), allowed, http.MethodPost, "/public/leads", "")
	h := rec.Header()
	if h.Get("Access-Control-Allow-Origin") != allowed || h.Get("Access-Control-Allow-Credentials") != "true" ||
		!strings.Contains(h.Get("Access-Control-Expose-Headers"), "Content-Disposition") {
		t.Errorf("a web host on the form's path answered %s", corsHeaders(rec))
	}
	pre := from(landingHandler(), allowed, http.MethodOptions, "/public/leads", http.MethodPost)
	if !strings.Contains(pre.Header().Get("Access-Control-Allow-Methods"), "DELETE") || !strings.Contains(pre.Header().Get("Access-Control-Allow-Headers"), "Authorization") {
		t.Errorf("a web host's preflight on the form's path answered %s", corsHeaders(pre))
	}
}

// TestWithoutAPublicOriginItsOperationsAdmitNobody fails when an unset public
// origin matches a request that sends no Origin, or any origin at all.
func TestWithoutAPublicOriginItsOperationsAdmitNobody(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	for name, h := range map[string]http.Handler{
		"no public origin":      httpx.CORS([]string{allowed})(inner),
		"an empty one":          httpx.CORS([]string{allowed}, httpx.PublicOrigin{Operations: landingOperations})(inner),
		"one with no operation": httpx.CORS([]string{allowed}, httpx.PublicOrigin{Origin: landing})(inner),
	} {
		for _, origin := range []string{"", landing} {
			if rec := from(h, origin, http.MethodGet, "/public/status", ""); corsHeaders(rec) != "" || rec.Code != http.StatusTeapot {
				t.Errorf("%s, Origin %q: %d %s", name, origin, rec.Code, corsHeaders(rec))
			}
			if rec := from(h, origin, http.MethodOptions, "/public/leads", http.MethodPost); corsHeaders(rec) != "" {
				t.Errorf("%s, Origin %q: the preflight answered %s", name, origin, corsHeaders(rec))
			}
		}
	}
}
