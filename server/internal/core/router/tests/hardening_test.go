package router_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"quizzivy/internal/core/router"
)

func TestBodyLimitPrecedesJSONValidation(t *testing.T) {
	for _, length := range []int64{-1, 2 << 20} {
		t.Run(strconv.FormatInt(length, 10), func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(strings.Repeat(" ", 2<<20)+`{"email":"student@example.com","password":"a-password"}`))
			req.ContentLength = length
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			newTestRouter(t, fakeDB{}).ServeHTTP(rec, req)
			if rec.Code != http.StatusRequestEntityTooLarge {
				t.Fatalf("status = %d, want 413; body = %s", rec.Code, rec.Body.String())
			}
		})
	}
}

// The browser protections every answer carries, by value: a header that is
// there with a weaker value passes a check that it is there.
var securityHeaders = map[string]string{
	"X-Content-Type-Options":    "nosniff",
	"X-Frame-Options":           "DENY",
	"Referrer-Policy":           "no-referrer",
	"Strict-Transport-Security": "max-age=31536000; includeSubDomains",
	"Content-Security-Policy":   "frame-ancestors 'none'; base-uri 'none'; object-src 'none'",
	"Permissions-Policy":        "camera=(), microphone=(), geolocation=(), payment=()",
}

// The two hosts the web is served from (spec §5.8): students and staff.
var webHosts = []string{"https://my.quizzivy.com", "https://portal.quizzivy.com"}

func wantSecurityHeaders(t *testing.T, answer string, got http.Header) {
	t.Helper()
	for header, want := range securityHeaders {
		if value := got.Get(header); value != want {
			t.Errorf("%s: %s = %q, want %q", answer, header, value, want)
		}
	}
}

func TestSecurityHeadersOnSuccessFailureAndPreflight(t *testing.T) {
	private := map[string]bool{"/auth/login": true, "/app/assignments": true, "/teacher/dashboard": true, "/admin/docs-session": true, "/me/summary": true}
	for _, path := range []string{"/livez", "/healthz", "/app/assignments", "/auth/login", "/teacher/dashboard", "/admin/docs-session", "/me/summary", "/missing"} {
		t.Run(path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			newTestRouter(t, fakeDB{}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
			wantSecurityHeaders(t, path, rec.Header())
			if private[path] && rec.Header().Get("Cache-Control") != "no-store" {
				t.Errorf("%s is cacheable: Cache-Control %q", path, rec.Header().Get("Cache-Control"))
			}
		})
	}
	window := underWay()
	window.over.Store(true)
	status := httptest.NewRecorder()
	gatedRouter(t, window, testIssuer(t)).ServeHTTP(status, httptest.NewRequest(http.MethodGet, "/public/status", nil))
	if got := status.Header().Get("Cache-Control"); got != "public, max-age=30" {
		t.Errorf("/public/status Cache-Control = %q, want its own public, max-age=30", got)
	}
}

// A preflight is answered by the CORS middleware and never reaches a handler,
// so it is the answer most likely to go out without the protections. It is
// asked from each host the web is served from, and from the one host it was
// served from until v0.13.5, which is no longer admitted.
func TestAPreflightFromEachWebHostCarriesTheSecurityHeaders(t *testing.T) {
	h, err := router.New(router.Deps{Principals: newFakePrincipals(), DB: fakeDB{}},
		slog.New(slog.NewTextHandler(io.Discard, nil)), webHosts, "")
	if err != nil {
		t.Fatal(err)
	}
	preflight := func(origin string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodOptions, "/auth/login", nil)
		request.Header.Set("Origin", origin)
		request.Header.Set("Access-Control-Request-Method", "POST")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, request)
		return rec
	}
	for _, host := range webHosts {
		rec := preflight(host)
		if rec.Code != http.StatusNoContent {
			t.Errorf("a preflight from %s answered %d, want 204", host, rec.Code)
		}
		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != host {
			t.Errorf("a preflight from %s: Access-Control-Allow-Origin = %q, want the host itself", host, got)
		}
		wantSecurityHeaders(t, "a preflight from "+host, rec.Header())
	}

	retired := preflight("https://app.quizzivy.com")
	if got := retired.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("a preflight from the retired host was admitted: Access-Control-Allow-Origin = %q", got)
	}
	wantSecurityHeaders(t, "a preflight that is refused", retired.Header())
}
