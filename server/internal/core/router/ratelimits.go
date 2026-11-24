package router

import (
	"strings"

	classesdomain "quizzivy/internal/modules/classes/domain"
	"quizzivy/internal/platform/ratelimit"
	"quizzivy/internal/shared/validation"
)

// RateLimits declares the policy for every public operation and for the in-app
// join, keyed by client address or by a field of the body. It is applied
// before authentication. The login buckets key on the lower-cased bare address
// the sign-in looks up, not on the string as written, so N <a@x.co>, "a"@x.co
// and a@x.co share one budget.
func RateLimits() *ratelimit.Registry {
	reg := ratelimit.NewRegistry()
	const capacity = 10_000
	const maxKeyBodyBytes = 8 * 1024
	joinCode := ratelimit.JSONFieldKeyFunc("joinCode", maxKeyBodyBytes, 9, classesdomain.JoinCodes.Normalize)
	email := ratelimit.JSONFieldKeyFunc("email", maxKeyBodyBytes, 254, canonicalEmail)
	linkToken := ratelimit.JSONFieldKey("token", maxKeyBodyBytes, 128)
	reg.Add("POST /join/preview", capacity, ratelimit.PerMinute(120), ratelimit.PerHour(600)).
		WithKey("perCode", joinCode, capacity, ratelimit.PerHour(200))
	reg.Add("POST /auth/google", capacity, ratelimit.PerMinute(120), ratelimit.PerHour(600)).
		WithKey("perCode", joinCode, capacity, ratelimit.PerHour(200))
	reg.Add("POST /auth/login", capacity, ratelimit.PerMinute(120), ratelimit.PerHour(600)).
		WithKey("perAddressAndEmail", ratelimit.Compose(ratelimit.Address, email), capacity, ratelimit.PerMinute(10)).
		WithKey("perEmail", email, capacity, ratelimit.PerHour(20))
	reg.Add("POST /auth/set-password/check", capacity, ratelimit.PerMinute(120), ratelimit.PerHour(1000)).
		WithKey("perToken", linkToken, capacity, ratelimit.PerHour(20))
	reg.Add("POST /auth/set-password", capacity, ratelimit.PerMinute(60), ratelimit.PerHour(600)).
		WithKey("perToken", linkToken, capacity, ratelimit.PerHour(10))
	reg.Add("POST /auth/refresh", capacity, ratelimit.PerMinute(120), ratelimit.PerHour(1200))
	reg.Add("POST /auth/logout", capacity, ratelimit.PerMinute(120), ratelimit.PerHour(1200))
	reg.Add("POST /app/classes/join", capacity, ratelimit.PerMinute(120), ratelimit.PerHour(600)).
		WithKey("perCode", joinCode, capacity, ratelimit.PerHour(200))
	reg.Add("POST /app/attempts/{id}/events", capacity, ratelimit.PerMinute(120))
	reg.Add("GET /public/status", capacity, ratelimit.PerMinute(120), ratelimit.PerHour(2000))
	reg.Add("GET /public/organization", capacity, ratelimit.PerMinute(120), ratelimit.PerHour(2000))
	reg.Add("GET /public/organization/logo", capacity, ratelimit.PerMinute(300))
	reg.Add("POST /public/leads", capacity, ratelimit.PerMinute(3), ratelimit.PerHour(10))
	// Resend's signed delivery events (T-R7.15), per sending address: a
	// bounce or complaint arrives one delivery at a time, and Resend retries a
	// refused one, so 120 a minute holds a burst without letting a caller
	// spend the signature check at will.
	reg.Add("POST /public/email-events", capacity, ratelimit.PerMinute(120))

	return reg
}

func canonicalEmail(value string) string {
	if address, err := validation.ParseEmail(value); err == nil {
		return strings.ToLower(address)
	}
	return strings.ToLower(strings.TrimSpace(value))
}

var aiSuggestions = []string{
	"POST /teacher/ai/questions/generate",
	"POST /teacher/ai/tests/{id}/check",
	"POST /teacher/ai/tests/{id}/variant",
	"POST /teacher/ai/questions/{id}/explanation",
	"POST /teacher/ai/questions/{id}/distractors",
	"POST /teacher/ai/gaps/distractors",
	"POST /teacher/ai/attempts/{attemptId}/answers/{questionId}/score-suggestion",
	"POST /teacher/ai/assignments/{id}/insights",
}

var aiConsoleWrites = []string{
	"PATCH /admin/ai/rules",
	"POST /admin/ai/credits/additions",
	"PUT /admin/ai/teachers/{userId}/allowance",
	"POST /admin/ai/credit-requests/{id}/approve",
	"POST /admin/ai/credit-requests/{id}/decline",
}

// PrincipalRateLimits declares the limits counted per signed-in user, wherever
// the user connects from: each entry has no per-address bucket and one bucket,
// perActor, keyed by ratelimit.PrincipalKey. They are applied after the
// permission gate, so an entry may name only an operation that requires a
// token; New refuses any other. The authenticated operations that mint a
// credential are here, so are the uploads that store a file per call, and so
// are the share dialog's directory of colleagues and its grants, and so are a
// staff member's messages, whose direct messages and announcements spend one
// budget, and the New message picker, which lists every student and every
// colleague. A colleague thread's posts have a budget of their own at the
// same numbers, and its list and its thread, which name every colleague, are
// read at most 60 times a minute (F2, T-F2.4).
func PrincipalRateLimits() *ratelimit.Registry {
	reg := ratelimit.NewRegistry()
	const capacity = 10_000
	perActor := func(pattern string, rules ...ratelimit.Rule) {
		reg.AddKeyed(pattern).WithKey("perActor", ratelimit.PrincipalKey, capacity, rules...)
	}
	perActor("POST /teacher/students/{id}/reset-password", ratelimit.PerMinute(5), ratelimit.PerHour(30))
	perActor("POST /teacher/students/reset-passwords", ratelimit.PerMinute(2), ratelimit.PerHour(10))
	perActor("POST /teacher/students", ratelimit.PerMinute(30), ratelimit.PerHour(300))
	perActor("POST /admin/users", ratelimit.PerMinute(30), ratelimit.PerHour(300))
	perActor("POST /admin/users/{id}/reset-password", ratelimit.PerMinute(5), ratelimit.PerHour(30))
	perActor("POST /admin/users/reset-passwords", ratelimit.PerMinute(2), ratelimit.PerHour(10))
	perActor("PUT /admin/users/{id}/role", ratelimit.PerMinute(30))
	perActor("POST /admin/users/{id}/disable", ratelimit.PerMinute(30))
	perActor("POST /admin/users/disable", ratelimit.PerMinute(10))
	perActor("POST /admin/users/{id}/transfer-content", ratelimit.PerMinute(10))
	perActor("POST /admin/user-imports/preview", ratelimit.PerMinute(10))
	perActor("POST /admin/user-imports", ratelimit.PerHour(5))
	perActor("POST /admin/user-imports/{id}/sheet", ratelimit.PerMinute(5))
	perActor("POST /teacher/classes/{id}/join-code", ratelimit.PerMinute(10), ratelimit.PerHour(60))
	perActor("GET /teacher/classes/{id}/join-code", ratelimit.PerMinute(60), ratelimit.PerHour(600))
	perActor("GET /teacher/classes", ratelimit.PerMinute(60), ratelimit.PerHour(600))
	perActor("GET /admin/classes", ratelimit.PerMinute(60), ratelimit.PerHour(600))
	perActor("POST /admin/docs-session", ratelimit.PerMinute(5), ratelimit.PerHour(30))
	perActor("GET /admin/api-reference", ratelimit.PerMinute(60), ratelimit.PerHour(600))
	perActor("GET /admin/api-reference/openapi.json", ratelimit.PerMinute(10), ratelimit.PerHour(60))
	perActor("GET /admin/audit-log", ratelimit.PerMinute(60), ratelimit.PerHour(600))
	perActor("GET /admin/audit-log/export", ratelimit.PerMinute(5), ratelimit.PerHour(30))
	perActor("POST /admin/data-exports", ratelimit.PerMinute(10))
	perActor("GET /admin/data-exports/{id}", ratelimit.PerMinute(60), ratelimit.PerHour(600))
	perActor("POST /admin/data-exports/{id}/download", ratelimit.PerHour(10))
	perActor("DELETE /auth/sessions/{familyId}", ratelimit.PerMinute(10), ratelimit.PerHour(60))
	perActor("POST /auth/sessions/revoke-others", ratelimit.PerMinute(5), ratelimit.PerHour(30))
	perActor("POST /auth/set-password/request", ratelimit.PerMinute(3), ratelimit.PerHour(10))
	perActor("GET /teacher/assignments/results.csv", ratelimit.PerMinute(10), ratelimit.PerHour(60))
	perActor("PUT /me/avatar", ratelimit.PerHour(10))
	perActor("POST /admin/settings/logo", ratelimit.PerHour(20))
	perActor("POST /teacher/media/{id}/replace", ratelimit.PerMinute(10), ratelimit.PerHour(60))
	perActor("GET /teacher/directory/teachers", ratelimit.PerMinute(60))
	perActor("POST /teacher/shares", ratelimit.PerHour(30))
	reg.Share("POST /teacher/messages/classes/{classId}", reg.AddKeyed("POST /teacher/messages/students/{studentId}").
		WithKey("perActor", ratelimit.PrincipalKey, capacity, ratelimit.PerMinute(30), ratelimit.PerDay(500)))
	perActor("POST /app/messages/teachers/{teacherId}", ratelimit.PerMinute(10), ratelimit.PerDay(200))
	perActor("POST /teacher/messages/attachments", ratelimit.PerHour(20))
	perActor("GET /teacher/messages/recipients", ratelimit.PerMinute(60))
	perActor("POST /teacher/messages/colleagues/{colleagueId}", ratelimit.PerMinute(30), ratelimit.PerDay(500))
	perActor("GET /teacher/messages/colleagues", ratelimit.PerMinute(60))
	perActor("GET /teacher/messages/colleagues/{colleagueId}", ratelimit.PerMinute(60))
	for _, pattern := range aiSuggestions {
		perActor(pattern, ratelimit.PerMinute(10), ratelimit.PerHour(100))
	}
	perActor("POST /teacher/ai/bank/cleanup", ratelimit.PerHour(3))
	perActor("POST /teacher/ai/credit-requests", ratelimit.PerHour(5))
	for _, pattern := range []string{"GET /teacher/ai/usage", "GET /admin/ai/credits"} {
		perActor(pattern, ratelimit.PerMinute(60), ratelimit.PerHour(600))
	}
	for _, pattern := range aiConsoleWrites {
		perActor(pattern, ratelimit.PerMinute(10), ratelimit.PerHour(100))
	}
	return reg
}

// ServiceRateLimits declares the policy for the routes served beside the
// contract, which the generated middleware chain never sees.
func ServiceRateLimits() *ratelimit.Registry {
	reg := ratelimit.NewRegistry()
	const capacity = 10_000
	reg.Add("GET /livez", capacity, ratelimit.PerMinute(30), ratelimit.PerHour(900))
	reg.Add("GET /healthz", capacity, ratelimit.PerMinute(10), ratelimit.PerHour(120))
	reg.Add("GET /docs", capacity, ratelimit.PerMinute(20), ratelimit.PerHour(200))
	reg.Add("GET /docs/openapi.json", capacity, ratelimit.PerMinute(20), ratelimit.PerHour(200))
	return reg
}
