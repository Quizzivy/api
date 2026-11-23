package router

import (
	"log/slog"

	"quizzivy/gen/openapi"
	accesshttp "quizzivy/internal/modules/access/http"
	aihttp "quizzivy/internal/modules/ai/http"
	assignmentshttp "quizzivy/internal/modules/assignments/http"
	attemptshttp "quizzivy/internal/modules/attempts/http"
	audithttp "quizzivy/internal/modules/audit/http"
	availabilityhttp "quizzivy/internal/modules/availability/http"
	classeshttp "quizzivy/internal/modules/classes/http"
	dashboardhttp "quizzivy/internal/modules/dashboard/http"
	exportshttp "quizzivy/internal/modules/exports/http"
	identitytoken "quizzivy/internal/modules/identity/application/token"
	identityhttp "quizzivy/internal/modules/identity/http"
	importshttp "quizzivy/internal/modules/imports/http"
	leadshttp "quizzivy/internal/modules/leads/http"
	mediahttp "quizzivy/internal/modules/media/http"
	messaginghttp "quizzivy/internal/modules/messaging/http"
	notificationshttp "quizzivy/internal/modules/notifications/http"
	organizationhttp "quizzivy/internal/modules/organization/http"
	questionshttp "quizzivy/internal/modules/questions/http"
	sharinghttp "quizzivy/internal/modules/sharing/http"
	testshttp "quizzivy/internal/modules/tests/http"
	timetablehttp "quizzivy/internal/modules/timetable/http"
	"quizzivy/internal/platform/email"
	"quizzivy/internal/platform/httpx"
)

// Server implements the generated StrictServerInterface by embedding each module's handlers.
type Server struct {
	accesshttp.Access
	aihttp.AI
	audithttp.Audit
	dashboardhttp.Dashboard
	classeshttp.Classes
	importshttp.Imports
	identityhttp.Identity
	questionshttp.Questions
	mediahttp.Media
	testshttp.Tests
	assignmentshttp.Assignments
	attemptshttp.Attempts
	availabilityhttp.Availability
	notificationshttp.Notifications
	organizationhttp.Organization
	exportshttp.Exports
	leadshttp.Leads
	sharinghttp.Sharing
	messaginghttp.Staff
	messaginghttp.Student
	timetablehttp.Timetable
	EmailEvents
	Deps Deps
	// Logger is nil in tests; read it through logOf.
	Logger *slog.Logger
}

// Deps is what handlers need. It grows as phases add capability.
type Deps struct {
	Modules    Modules
	DB         DB
	Tokens     TokenVerifier
	Docs       *identitytoken.Issuer
	DocsPublic bool
	// Maintenance is what the maintenance gate asks; nil means no gate.
	Maintenance httpx.MaintenanceSource
	// Principals resolves who a request acts as; New refuses a nil one.
	Principals httpx.PrincipalResolver
	// LandingOrigins are the landing site's origins (LANDING_URL), each of
	// which may call LandingOperations without credentials; none admits none.
	LandingOrigins []string
	// EmailWebhook receives Resend's delivery events; nil without
	// RESEND_WEBHOOK_SECRET, when receiveEmailEvents answers 501.
	EmailWebhook *email.Webhook
}

var _ openapi.StrictServerInterface = (*Server)(nil)
