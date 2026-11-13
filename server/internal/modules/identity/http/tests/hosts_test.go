package http_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"quizzivy/gen/openapi"
	"quizzivy/internal/modules/identity/application"
	"quizzivy/internal/modules/identity/application/command"
	"quizzivy/internal/modules/identity/application/model"
	"quizzivy/internal/modules/identity/domain"
	identityhttp "quizzivy/internal/modules/identity/http"
	"quizzivy/internal/platform/httpx"
	"quizzivy/internal/shared/access"
	"quizzivy/internal/shared/cqrs"
)

const (
	studentOrigin = "https://my.quizzivy.com"
	staffOrigin   = "https://portal.quizzivy.com"
)

var twoHosts = access.Hosts{Student: studentOrigin, Staff: staffOrigin}

var cookieOf = map[access.Audience]string{
	access.AudienceAny:     "quizzivy_refresh",
	access.AudienceStudent: "quizzivy_refresh_student",
	access.AudienceStaff:   "quizzivy_refresh_staff",
}

var originOf = map[access.Audience]string{
	access.AudienceAny:     "https://preview.pages.dev",
	access.AudienceStudent: studentOrigin,
	access.AudienceStaff:   staffOrigin,
}

type hostAuth struct {
	loginErr  error
	audience  access.Audience
	presented string
}

func (f *hostAuth) transport() identityhttp.Identity {
	login := func(_ context.Context, cmd command.Login) (model.Session, error) {
		f.audience = cmd.Audience
		return model.Session{AccessToken: "access", ExpiresIn: 900, RefreshToken: "issued"}, f.loginErr
	}
	refresh := func(_ context.Context, cmd command.Refresh) (model.RefreshResult, error) {
		f.audience, f.presented = cmd.Audience, cmd.Token
		return model.RefreshResult{AccessToken: "access", ExpiresIn: 900, RefreshToken: "next"}, nil
	}
	logout := func(_ context.Context, cmd command.Logout) (cqrs.Nothing, error) {
		f.presented = cmd.Token
		return cqrs.Nothing{}, nil
	}
	app := &application.Application{Commands: application.Commands{
		Login:   cqrs.HandlerFunc[command.Login, model.Session](login),
		Refresh: cqrs.HandlerFunc[command.Refresh, model.RefreshResult](refresh),
		Logout:  cqrs.HandlerFunc[command.Logout, cqrs.Nothing](logout),
	}}
	return identityhttp.NewIdentity(app, time.Hour, true, nil).WithHosts(twoHosts)
}

func everyRefreshCookie(req *http.Request) {
	for audience, name := range cookieOf {
		req.AddCookie(&http.Cookie{Name: name, Value: "token-of-" + string(audience)})
	}
}

func setCookies(t *testing.T, headers ...string) map[string]*http.Cookie {
	t.Helper()
	out := map[string]*http.Cookie{}
	for _, header := range headers {
		c, err := http.ParseSetCookie(header)
		if err != nil {
			t.Fatalf("Set-Cookie %q does not parse: %v", header, err)
		}
		out[c.Name] = c
	}
	return out
}

// TestASignInSetsTheCookieOfTheHostItCameFrom fails when sign-in sets one
// cookie name for every origin, which is what lets two hosts share a session,
// and when the command is not told which host the request came from.
func TestASignInSetsTheCookieOfTheHostItCameFrom(t *testing.T) {
	for audience, origin := range originOf {
		fake := &hostAuth{}
		h := fake.transport()
		req := httptest.NewRequest(http.MethodPost, "/auth/login", nil)
		req.Header.Set("Origin", origin)
		var resp openapi.LoginResponseObject
		h.WithRefreshCookie(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			resp, _ = h.Login(r.Context(), openapi.LoginRequestObject{
				Body: &openapi.LoginJSONRequestBody{Email: openapi_types.Email("a@example.com"), Password: "mật-khẩu"},
			})
		})).ServeHTTP(httptest.NewRecorder(), req)
		ok, isOK := resp.(openapi.Login200JSONResponse)
		if !isOK || ok.Headers.SetCookie == nil {
			t.Fatalf("%s: Login answered %T without a Set-Cookie header", audience, resp)
		}
		set := setCookies(t, *ok.Headers.SetCookie)
		c := set[cookieOf[audience]]
		if len(set) != 1 || c == nil || c.Value != "issued" || c.Path != "/auth" || !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteLaxMode || c.Domain != "" {
			t.Errorf("%s: sign-in set %+v, want %s alone with the documented attributes", audience, set, cookieOf[audience])
		}
		if fake.audience != audience {
			t.Errorf("%s: the sign-in was told the request came from %q", audience, fake.audience)
		}
	}
}

// TestARefreshReadsOnlyTheCookieOfItsOwnHost fails when the middleware reads
// a cookie by any name it finds: with all three cookies in the browser, a
// request from one host must present that host's token and replace that
// host's cookie.
func TestARefreshReadsOnlyTheCookieOfItsOwnHost(t *testing.T) {
	for audience, origin := range originOf {
		fake := &hostAuth{}
		h := fake.transport()
		req := httptest.NewRequest(http.MethodPost, "/auth/refresh", nil)
		req.Header.Set("Origin", origin)
		everyRefreshCookie(req)
		var resp openapi.RefreshSessionResponseObject
		h.WithRefreshCookie(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			resp, _ = h.RefreshSession(r.Context(), openapi.RefreshSessionRequestObject{})
		})).ServeHTTP(httptest.NewRecorder(), req)
		if fake.presented != "token-of-"+string(audience) || fake.audience != audience {
			t.Errorf("%s: the refresh presented %q as a session of %q", audience, fake.presented, fake.audience)
		}
		ok, isOK := resp.(openapi.RefreshSession200JSONResponse)
		if !isOK || ok.Headers.SetCookie == nil {
			t.Fatalf("%s: the refresh answered %T", audience, resp)
		}
		if set := setCookies(t, *ok.Headers.SetCookie); len(set) != 1 || set[cookieOf[audience]] == nil || set[cookieOf[audience]].Value != "next" {
			t.Errorf("%s: the refresh replaced %+v, want %s alone", audience, set, cookieOf[audience])
		}
	}

	fake := &hostAuth{}
	h := fake.transport()
	req := httptest.NewRequest(http.MethodPost, "/auth/refresh", nil)
	req.Header.Set("Origin", studentOrigin)
	req.AddCookie(&http.Cookie{Name: cookieOf[access.AudienceStaff], Value: "staff-token"})
	req.AddCookie(&http.Cookie{Name: cookieOf[access.AudienceAny], Value: "old-token"})
	h.WithRefreshCookie(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_, _ = h.RefreshSession(r.Context(), openapi.RefreshSessionRequestObject{})
	})).ServeHTTP(httptest.NewRecorder(), req)
	if fake.presented != "" {
		t.Errorf("the student host, with no session of its own, presented %q from another host's cookie", fake.presented)
	}
}

// TestSigningOutEndsOnlyTheSessionOfItsOwnHost fails when a sign-out clears
// another host's cookie, and when the student host's sign-out closes the Admin
// console's API reference session.
func TestSigningOutEndsOnlyTheSessionOfItsOwnHost(t *testing.T) {
	for audience, origin := range originOf {
		fake := &hostAuth{}
		h := fake.transport()
		req := httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
		req.Header.Set("Origin", origin)
		everyRefreshCookie(req)
		var resp openapi.LogoutResponseObject
		h.WithRefreshCookie(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			resp, _ = h.Logout(r.Context(), openapi.LogoutRequestObject{})
		})).ServeHTTP(httptest.NewRecorder(), req)
		rec := httptest.NewRecorder()
		if err := resp.VisitLogoutResponse(rec); err != nil || rec.Code != http.StatusNoContent {
			t.Fatalf("%s: the sign-out rendered %d: %v", audience, rec.Code, err)
		}
		if fake.presented != "token-of-"+string(audience) {
			t.Errorf("%s: the sign-out revoked %q", audience, fake.presented)
		}
		cleared := setCookies(t, rec.Header().Values("Set-Cookie")...)
		for other, name := range cookieOf {
			c := cleared[name]
			if other == audience && (c == nil || c.Value != "" || c.MaxAge >= 0 || c.Path != "/auth" || !c.HttpOnly || !c.Secure) {
				t.Errorf("%s: its own cookie is not cleared: %+v", audience, c)
			}
			if other != audience && c != nil {
				t.Errorf("%s: the sign-out cleared %s, the cookie of another host's session", audience, name)
			}
		}
		if docs := cleared["quizzivy_docs"]; (docs != nil) != (audience != access.AudienceStudent) {
			t.Errorf("%s: the docs cookie cleared = %t", audience, docs != nil)
		}

		refused := httptest.NewRecorder()
		h.ClearSessionOnRefusedLogout(refused, req)
		if got := setCookies(t, refused.Header().Values("Set-Cookie")...); len(got) != len(cleared) || got[cookieOf[audience]] == nil {
			t.Errorf("%s: a refused sign-out cleared %v, want what a sign-out clears", audience, refused.Header().Values("Set-Cookie"))
		}
	}

	h := (&hostAuth{}).transport()
	req := httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
	req.Header.Set("Origin", studentOrigin)
	req.AddCookie(&http.Cookie{Name: cookieOf[access.AudienceStaff], Value: "staff-token"})
	rec := httptest.NewRecorder()
	h.ClearSessionOnRefusedLogout(rec, req)
	if got := rec.Header().Values("Set-Cookie"); len(got) != 0 {
		t.Errorf("a refused sign-out at the student host, which holds no session, cleared %v", got)
	}
}

// TestAnAccountAtTheWrongHostIsToldWhereToSignIn fails when the refusal loses
// its 403 FORBIDDEN, names the host the request came from instead of the
// other, or sets a cookie.
func TestAnAccountAtTheWrongHostIsToldWhereToSignIn(t *testing.T) {
	for origin, want := range map[string][2]string{
		staffOrigin:   {"Đây là tài khoản học viên. Hãy đăng nhập tại my.quizzivy.com.", "This is a student account. Sign in at my.quizzivy.com."},
		studentOrigin: {"Tài khoản này dành cho giáo viên và quản trị viên. Hãy đăng nhập tại portal.quizzivy.com.", "This account is for teachers and admins. Sign in at portal.quizzivy.com."},
	} {
		for i, language := range []string{"vi", "en"} {
			h := (&hostAuth{loginErr: domain.ErrWrongHost}).transport()
			req := httptest.NewRequest(http.MethodPost, "/auth/login", nil)
			req.Header.Set("Origin", origin)
			req.Header.Set("Accept-Language", language)
			var resp openapi.LoginResponseObject
			httpx.WithRequestMeta(func(*http.Request) string { return "203.0.113.9" })(h.WithRefreshCookie(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				resp, _ = h.Login(r.Context(), openapi.LoginRequestObject{
					Body: &openapi.LoginJSONRequestBody{Email: openapi_types.Email("a@example.com"), Password: "mật-khẩu"},
				})
			}))).ServeHTTP(httptest.NewRecorder(), req)
			refused, ok := resp.(openapi.Login403JSONResponse)
			if !ok {
				t.Fatalf("%s: a wrong host answered %T, want the 403", origin, resp)
			}
			if refused.Error.Code != openapi.FORBIDDEN || refused.Error.Message != want[i] {
				t.Errorf("%s in %s: %s %q, want FORBIDDEN %q", origin, language, refused.Error.Code, refused.Error.Message, want[i])
			}
			rec := httptest.NewRecorder()
			if err := resp.VisitLoginResponse(rec); err != nil || len(rec.Header().Values("Set-Cookie")) != 0 {
				t.Errorf("%s: the refusal set a cookie: %v %v", origin, rec.Header().Values("Set-Cookie"), err)
			}
		}
	}
}

// TestEveryRefreshCookieIsInTheContract fails when the API sets a cookie the
// contract does not declare, or declares one under another name.
func TestEveryRefreshCookieIsInTheContract(t *testing.T) {
	spec, err := openapi.GetSpec()
	if err != nil {
		t.Fatalf("GetSpec: %v", err)
	}
	for scheme, audience := range map[string]access.Audience{
		"refreshCookie": access.AudienceAny, "studentRefreshCookie": access.AudienceStudent, "staffRefreshCookie": access.AudienceStaff,
	} {
		declared, ok := spec.Components.SecuritySchemes[scheme]
		if !ok || declared.Value == nil {
			t.Fatalf("api/openapi.yaml defines no %s security scheme", scheme)
		}
		if declared.Value.In != "cookie" || declared.Value.Name != cookieOf[audience] {
			t.Errorf("%s is %q in %q, and the API sets %q", scheme, declared.Value.Name, declared.Value.In, cookieOf[audience])
		}
	}
	for _, path := range []string{"/auth/refresh", "/auth/logout"} {
		op := spec.Paths.Find(path).Post
		var named []string
		for _, requirement := range *op.Security {
			for scheme := range requirement {
				named = append(named, scheme)
			}
		}
		if got := strings.Join(named, " "); len(named) != 3 || !strings.Contains(got, "studentRefreshCookie") || !strings.Contains(got, "staffRefreshCookie") || !strings.Contains(got, "refreshCookie") {
			t.Errorf("%s accepts %v, want any one of the three refresh cookies", path, named)
		}
	}
}
