package httpx_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"quizzivy/internal/platform/httpx"
	"quizzivy/internal/shared/access"
)

var treeRequirements = map[string]access.Requirement{
	"GET /app/assignments":     access.AnyOf(access.LearningTakeTests),
	"GET /teacher/tests":       access.AnyOf(access.WorkspaceTeacher),
	"GET /admin/users":         access.AnyOf(access.WorkspaceAdmin),
	"GET /auth/me":             access.AnyOf(access.Self),
	"GET /me/notifications":    access.AnyOf(access.Self),
	"GET /application/unknown": access.AnyOf(access.Self),
}

// TestASessionOfOneHostIsRefusedByTheOtherHostsOperations fails when
// RequirePermission stops reading the session's audience: the account here
// holds every permission, so only the host of its session decides.
func TestASessionOfOneHostIsRefusedByTheOtherHostsOperations(t *testing.T) {
	both := resolver{"both": {UserID: "both", Permissions: access.NewSet(access.LearningTakeTests, access.TeachingGrading, access.ScopeAll)}}
	gate := func(audience access.Audience) http.Handler {
		verify := func(raw string) (httpx.Principal, error) {
			return httpx.Principal{UserID: raw, Audience: audience}, nil
		}
		return httpx.RequireAuth(nil, verify)(httpx.RequirePermission(treeRequirements, both)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		})))
	}
	const ok, refused = http.StatusNoContent, http.StatusForbidden
	for pattern, want := range map[string]map[access.Audience]int{
		"GET /app/assignments":     {access.AudienceStudent: ok, access.AudienceStaff: refused, access.AudienceAny: ok, "": ok},
		"GET /teacher/tests":       {access.AudienceStudent: refused, access.AudienceStaff: ok, access.AudienceAny: ok, "": ok},
		"GET /admin/users":         {access.AudienceStudent: refused, access.AudienceStaff: ok, access.AudienceAny: ok, "": ok},
		"GET /auth/me":             {access.AudienceStudent: ok, access.AudienceStaff: ok, access.AudienceAny: ok, "": ok},
		"GET /me/notifications":    {access.AudienceStudent: ok, access.AudienceStaff: ok, access.AudienceAny: ok, "": ok},
		"GET /application/unknown": {access.AudienceStudent: ok, access.AudienceStaff: ok, access.AudienceAny: ok, "": ok},
	} {
		for audience, status := range want {
			method, path, _ := strings.Cut(pattern, " ")
			req := httptest.NewRequest(method, path, nil)
			req.Pattern = pattern
			req.Header.Set("Authorization", "Bearer both")
			rec := httptest.NewRecorder()
			gate(audience).ServeHTTP(rec, req)
			if rec.Code != status {
				t.Errorf("%s with a session of %q answered %d, want %d", pattern, audience, rec.Code, status)
			}
			if status == refused && !strings.Contains(rec.Body.String(), `"FORBIDDEN"`) {
				t.Errorf("%s with a session of %q refused without FORBIDDEN: %s", pattern, audience, rec.Body.String())
			}
		}
	}
}
