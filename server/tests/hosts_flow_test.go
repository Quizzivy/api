//go:build e2e

package e2e

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"quizzivy/internal/platform/config"
	"quizzivy/internal/shared/access"
)

// The two web hosts, over HTTP, through the whole stack: what the Origin
// header decides, with one cookie jar standing for one browser.

const (
	studentOrigin = "https://my.quizzivy.test"
	staffOrigin   = "https://portal.quizzivy.test"
)

func twoHosts(cfg *config.Config) {
	cfg.Hosts = access.Hosts{Student: studentOrigin, Staff: staffOrigin}
	cfg.AllowedOrigins = []string{studentOrigin, staffOrigin}
}

func (c *client) from(origin, method, path string, payload any) (int, http.Header, map[string]any) {
	c.w.t.Helper()
	return c.callWith(map[string]string{"Origin": origin}, method, path, payload)
}

func (c *client) signInFrom(origin, email, password string) (status int, header http.Header, body map[string]any) {
	c.w.t.Helper()
	c.token = ""
	status, header, body = c.from(origin, http.MethodPost, "/auth/login", map[string]any{"email": email, "password": password})
	if status == http.StatusOK {
		c.token = body["accessToken"].(string)
	}
	return status, header, body
}

func (c *client) refreshFrom(origin string) (int, string) {
	c.w.t.Helper()
	c.token = ""
	status, _, body := c.from(origin, http.MethodPost, "/auth/refresh", nil)
	if status != http.StatusOK {
		return status, ""
	}
	return status, body["accessToken"].(string)
}

func (c *client) cookieNames() []string {
	c.w.t.Helper()
	base, err := url.Parse(c.w.server.URL + "/auth/refresh")
	if err != nil {
		c.w.t.Fatal(err)
	}
	var names []string
	for _, cookie := range c.http.Jar.Cookies(base) {
		names = append(names, cookie.Name)
	}
	slices.Sort(names)
	return names
}

func audienceIn(t *testing.T, token string) string {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("the access token is not a JWT: %q", token)
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	var claims struct {
		Audience []string `json:"aud"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		t.Fatal(err)
	}
	return strings.Join(claims.Audience, ",")
}

// TestOneBrowserHoldsAStudentSessionAndAStaffSessionSideBySide proves, with a
// real server, what no layer below can: that the Origin header alone keeps
// two sessions of one account apart in one cookie jar, from sign-in through
// refresh and the permission gate to sign-out.
func TestOneBrowserHoldsAStudentSessionAndAStaffSessionSideBySide(t *testing.T) {
	w := boot(t, twoHosts)
	email, password := w.staffHolding("learning.take_tests", "teaching.grading")
	b := w.browser()

	if status, _, body := b.signInFrom(studentOrigin, email, password); status != http.StatusOK {
		t.Fatalf("sign-in at the student host: %d %v", status, body)
	}
	student := b.token
	if status, _, body := b.signInFrom(staffOrigin, email, password); status != http.StatusOK {
		t.Fatalf("sign-in at the staff host: %d %v", status, body)
	}
	staff := b.token
	if got := b.cookieNames(); !slices.Equal(got, []string{"quizzivy_refresh_staff", "quizzivy_refresh_student"}) {
		t.Fatalf("the browser holds %v, want one cookie per host", got)
	}
	if audienceIn(t, student) != "student" || audienceIn(t, staff) != "staff" {
		t.Fatalf("the access tokens are of %q and %q", audienceIn(t, student), audienceIn(t, staff))
	}

	for _, tc := range []struct {
		name, token, path string
		want              int
	}{
		{"the student session reads the student's assignments", student, "/app/assignments", http.StatusOK},
		{"the student session is kept out of the teacher workspace", student, "/teacher/tests", http.StatusForbidden},
		{"the staff session reads the teacher workspace", staff, "/teacher/tests", http.StatusOK},
		{"the staff session is kept out of the student app", staff, "/app/assignments", http.StatusForbidden},
		{"the student session reads its own account", student, "/auth/me", http.StatusOK},
		{"the staff session reads its own account", staff, "/auth/me", http.StatusOK},
	} {
		if status, _ := bearerOnly(w, tc.token).call(http.MethodGet, tc.path, nil); status != tc.want {
			t.Errorf("%s: %d, want %d", tc.name, status, tc.want)
		}
	}

	if status, token := b.refreshFrom(studentOrigin); status != http.StatusOK || audienceIn(t, token) != "student" {
		t.Fatalf("the student host's refresh: %d, a token of %q", status, audienceIn(t, token))
	}
	if status, token := b.refreshFrom(staffOrigin); status != http.StatusOK || audienceIn(t, token) != "staff" {
		t.Fatalf("the staff host's refresh: %d, a token of %q", status, audienceIn(t, token))
	}
	if status, _ := b.refreshFrom("https://preview.pages.test"); status != http.StatusUnauthorized {
		t.Errorf("an origin that is neither host refreshed a host's session: %d", status)
	}

	for origin, want := range map[string]int{studentOrigin: 1, staffOrigin: 1} {
		b.token = map[string]string{studentOrigin: student, staffOrigin: staff}[origin]
		status, _, body := b.from(origin, http.MethodGet, sessionsPath, nil)
		if items, _ := body["items"].([]any); status != http.StatusOK || len(items) != want {
			t.Errorf("the session list at %s: %d with %d rows, want its own session alone", origin, status, len(items))
		}
	}

	b.token = ""
	if status, _, _ := b.from(studentOrigin, http.MethodPost, "/auth/logout", nil); status != http.StatusNoContent {
		t.Fatalf("sign-out at the student host: %d", status)
	}
	if got := b.cookieNames(); !slices.Equal(got, []string{"quizzivy_refresh_staff"}) {
		t.Errorf("after the student host's sign-out the browser holds %v, want the staff cookie alone", got)
	}
	if status, _ := b.refreshFrom(studentOrigin); status != http.StatusUnauthorized {
		t.Errorf("the student host refreshed after its sign-out: %d", status)
	}
	if status, _ := b.refreshFrom(staffOrigin); status != http.StatusOK {
		t.Errorf("the staff session ended with the student host's sign-out: %d", status)
	}
}

// TestAnAccountIsTurnedAwayAtTheOtherHostOverHTTP proves the refusal as a
// browser meets it: the 403 FORBIDDEN envelope naming the other host, no
// cookie, and the same account admitted at its own host and at an origin that
// is neither.
func TestAnAccountIsTurnedAwayAtTheOtherHostOverHTTP(t *testing.T) {
	w := boot(t, twoHosts)
	accounts := map[string]struct {
		email, password, home, away, names string
	}{}
	studentEmail, studentPassword := w.staffHolding("learning.take_tests")
	teacherEmail, teacherPassword := w.createStaff("teacher")
	accounts["a student"] = struct{ email, password, home, away, names string }{studentEmail, studentPassword, studentOrigin, staffOrigin, "my.quizzivy.test"}
	accounts["a teacher"] = struct{ email, password, home, away, names string }{teacherEmail, teacherPassword, staffOrigin, studentOrigin, "portal.quizzivy.test"}

	for name, account := range accounts {
		b := w.browser()
		status, header, body := b.signInFrom(account.away, account.email, account.password)
		if status != http.StatusForbidden || b.errorCode(body) != "FORBIDDEN" {
			t.Fatalf("%s at the other host: %d %v, want 403 FORBIDDEN", name, status, body)
		}
		if message, _ := body["error"].(map[string]any)["message"].(string); !strings.Contains(message, account.names) {
			t.Errorf("%s: the refusal %q does not name %s", name, message, account.names)
		}
		if len(header.Values("Set-Cookie")) != 0 || len(b.cookieNames()) != 0 {
			t.Errorf("%s: the refusal set a cookie: %v", name, header.Values("Set-Cookie"))
		}
		if status, _, body := b.signInFrom(account.away, account.email, "sai-mat-khau-"+nonce(t)); status != http.StatusUnauthorized || b.errorCode(body) != "INVALID_CREDENTIALS" {
			t.Errorf("%s at the other host with a wrong password: %d %v, want what every wrong password gets", name, status, body)
		}
		if status, _, body := b.signInFrom(account.home, account.email, account.password); status != http.StatusOK {
			t.Errorf("%s at its own host: %d %v", name, status, body)
		}
		if status, _, body := w.browser().signInFrom("https://preview.pages.test", account.email, account.password); status != http.StatusOK {
			t.Errorf("%s at an origin that is neither host: %d %v", name, status, body)
		}
		if status, body := w.browser().call(http.MethodPost, "/auth/login", map[string]any{"email": account.email, "password": account.password}); status != http.StatusOK {
			t.Errorf("%s with no Origin: %d %v", name, status, body)
		}
	}
}
