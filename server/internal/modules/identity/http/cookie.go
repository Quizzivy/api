package http

import (
	"context"
	"net/http"
	"time"

	"quizzivy/internal/shared/access"
)

const refreshCookieName = "quizzivy_refresh"

type apiCtxKey int

const sessionKey apiCtxKey = 0

type presented struct {
	token    string
	audience access.Audience
}

func refreshCookieNameOf(audience access.Audience) string {
	switch audience {
	case access.AudienceStudent, access.AudienceStaff:
		return refreshCookieName + "_" + string(audience)
	default:
		return refreshCookieName
	}
}

// AudienceOf returns the audience of r: the web host its Origin header names.
func (h Identity) AudienceOf(r *http.Request) access.Audience {
	return h.hosts.AudienceOf(r.Header.Get("Origin"))
}

// WithRefreshCookie lifts the audience of the request, and the refresh cookie
// of that audience, into the request context. The cookie of another audience
// is never read.
func (h Identity) WithRefreshCookie(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := presented{audience: h.AudienceOf(r)}
		if c, err := r.Cookie(refreshCookieNameOf(p.audience)); err == nil {
			p.token = c.Value
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), sessionKey, p)))
	})
}

func refreshTokenFromContext(ctx context.Context) string {
	p, _ := ctx.Value(sessionKey).(presented)
	return p.token
}

func audienceFromContext(ctx context.Context) access.Audience {
	if p, ok := ctx.Value(sessionKey).(presented); ok {
		return p.audience
	}
	return access.AudienceAny
}

func refreshCookie(audience access.Audience, token string, ttl time.Duration, secure bool) *http.Cookie {
	return &http.Cookie{
		Name:     refreshCookieNameOf(audience),
		Value:    token,
		Path:     "/auth",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(ttl.Seconds()),
	}
}

type clearedSession []*http.Cookie

func (c clearedSession) VisitLogoutResponse(w http.ResponseWriter) error {
	for _, cookie := range c {
		w.Header().Add("Set-Cookie", cookie.String())
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

type failedLogout struct {
	cleared clearedSession
	err     error
}

func (f failedLogout) VisitLogoutResponse(w http.ResponseWriter) error {
	for _, cookie := range f.cleared {
		w.Header().Add("Set-Cookie", cookie.String())
	}
	return f.err
}

// ClearSessionOnRefusedLogout adds the cookie clears of a sign-out to the
// answer of a POST /auth/logout that is refused before its handler runs, by
// the maintenance gate or the rate limiter, when the request presented the
// refresh cookie of its audience, so the device is signed out although the
// refresh family is not revoked.
func (h Identity) ClearSessionOnRefusedLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.Path != "/auth/logout" {
		return
	}
	audience := h.AudienceOf(r)
	if c, err := r.Cookie(refreshCookieNameOf(audience)); err != nil || c.Value == "" {
		return
	}
	for _, cookie := range h.signedOut(audience) {
		w.Header().Add("Set-Cookie", cookie.String())
	}
}

func (h Identity) signedOut(audience access.Audience) clearedSession {
	cleared := clearedSession{clearRefreshCookie(audience, h.cookieSecure)}
	if audience != access.AudienceStudent {
		cleared = append(cleared, clearDocsCookie())
	}
	return cleared
}

func clearRefreshCookie(audience access.Audience, secure bool) *http.Cookie {
	return &http.Cookie{
		Name:     refreshCookieNameOf(audience),
		Value:    "",
		Path:     "/auth",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	}
}
