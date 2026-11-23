package http_test

import (
	"context"
	"errors"
	"quizzivy/gen/openapi"
	"quizzivy/internal/modules/dashboard/application"
	"quizzivy/internal/modules/dashboard/application/query"
	"quizzivy/internal/modules/dashboard/domain"
	dashboardhttp "quizzivy/internal/modules/dashboard/http"
	notificationsdomain "quizzivy/internal/modules/notifications/domain"
	"quizzivy/internal/platform/httpx"
	"quizzivy/internal/shared/access"
	"quizzivy/internal/shared/cqrs"
	"testing"
	"time"
)

func TestDashboardRangeAndNewFieldsReachTheResponse(t *testing.T) {
	for _, value := range []string{"", "7d", "14d", "30d"} {
		t.Run(value, func(t *testing.T) {
			var got query.Summary
			now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
			average := 63
			app := &application.Application{Queries: application.Queries{Summary: cqrs.HandlerFunc[query.Summary, domain.Summary](func(_ context.Context, q query.Summary) (domain.Summary, error) {
				got = q
				return domain.Summary{Home: domain.Home{TakingNow: domain.TakingNow{Students: 2, Assignments: 3}, Submissions: domain.Submissions{Days: []domain.Day{{Date: "2026-10-05", Count: 4}}, Total: 4, AveragePercent: &average}, Today: []domain.Today{{Kind: "opens", At: now, AssignmentID: principals()["an Admin"].UserID, Title: "paper", NotSubmitted: 2}}, RecentActivity: []domain.Activity{{Kind: "started", At: now, StudentName: "student", Subject: "paper", Flagged: true}}}}, nil
			})}}
			req := openapi.GetDashboardRequestObject{}
			if value != "" {
				v := openapi.GetDashboardParamsRange(value)
				req.Params.Range = &v
			}
			resp, err := dashboardhttp.NewDashboard(app).GetDashboard(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			want := value
			if want == "" {
				want = "14d"
			}
			if got.Range != want {
				t.Fatalf("range=%q want%q", got.Range, want)
			}
			out := resp.(openapi.GetDashboard200JSONResponse)
			if len(out.Today) != 1 {
				t.Fatalf("today=%+v", out.Today)
			}
			// The union's discriminator reads kind, so an item without it
			// would fail response validation.
			if kind, err := out.Today[0].Discriminator(); err != nil || kind != "opens" {
				t.Fatalf("today's kind=%q (%v), want opens", kind, err)
			}
			today, err := out.Today[0].AsDashboardTodayAssignment()
			if err != nil {
				t.Fatal(err)
			}
			if out.TakingNow.Students != 2 || out.TakingNow.Assignments != 3 || today.NotSubmitted != 2 || len(out.RecentActivity) != 1 || !out.RecentActivity[0].Flagged || out.Submissions.Total != 4 || out.Submissions.AveragePercent == nil || *out.Submissions.AveragePercent != 63 || out.Submissions.Days[0].Date.Format("2006-01-02") != "2026-10-05" {
				t.Fatalf("response=%+v", out)
			}
		})
	}
}

func TestNavAlwaysUsesOwnScopeAndResolvedGradingPermission(t *testing.T) {
	people := principals()
	people["without grading"] = access.Principal{UserID: people["a Teacher"].UserID, Permissions: access.NewSet(access.ContentTestsWrite)}
	for name, principal := range people {
		t.Run(name, func(t *testing.T) {
			var got query.Nav
			app := &application.Application{Queries: application.Queries{Nav: cqrs.HandlerFunc[query.Nav, domain.Nav](func(_ context.Context, q query.Nav) (domain.Nav, error) { got = q; return domain.Nav{}, nil })}}
			if _, err := dashboardhttp.NewDashboard(app).GetTeacherSummary(gated(t, principal, "GET /teacher/summary"), openapi.GetTeacherSummaryRequestObject{}); err != nil {
				t.Fatal(err)
			}
			if got.Scope != (access.Scope{UserID: principal.UserID}) || got.CanGrade != principal.Permissions.Has(access.TeachingGrading) {
				t.Fatalf("nav query=%+v", got)
			}
		})
	}
}

func TestNavCarriesTheAudienceTheRequestNamesAndRefusesAnUnknownOne(t *testing.T) {
	teacher := principals()["a Teacher"]
	var got query.Nav
	app := &application.Application{Queries: application.Queries{Nav: cqrs.HandlerFunc[query.Nav, domain.Nav](func(_ context.Context, q query.Nav) (domain.Nav, error) {
		got = q
		return domain.Nav{}, nil
	})}}
	transport := dashboardhttp.NewDashboard(app)
	for given, want := range map[string]notificationsdomain.Audience{"": "", "teacher": notificationsdomain.AudienceTeacher, "student": notificationsdomain.AudienceStudent} {
		params := openapi.GetTeacherSummaryParams{}
		if given != "" {
			audience := openapi.NotificationAudience(given)
			params.Audience = &audience
		}
		if _, err := transport.GetTeacherSummary(gated(t, teacher, "GET /teacher/summary"), openapi.GetTeacherSummaryRequestObject{Params: params}); err != nil {
			t.Fatal(err)
		}
		if got.Audience != want {
			t.Errorf("audience %q reached the query as %q, want %q", given, got.Audience, want)
		}
	}

	refusing := &application.Application{Queries: application.Queries{Nav: cqrs.HandlerFunc[query.Nav, domain.Nav](func(context.Context, query.Nav) (domain.Nav, error) {
		return domain.Nav{}, notificationsdomain.ErrUnknownAudience
	})}}
	unknown := openapi.NotificationAudience("admin")
	out, err := dashboardhttp.NewDashboard(refusing).GetTeacherSummary(gated(t, teacher, "GET /teacher/summary"),
		openapi.GetTeacherSummaryRequestObject{Params: openapi.GetTeacherSummaryParams{Audience: &unknown}})
	if err != nil {
		t.Fatal(err)
	}
	if _, refused := out.(openapi.GetTeacherSummary400JSONResponse); !refused {
		t.Errorf("an unknown audience was answered %T, want a 400", out)
	}
}

func TestMissingNavPortMapsToNotImplemented(t *testing.T) {
	var seen access.Scope
	app := application.New(recordingHome{seen: &seen})
	if _, err := dashboardhttp.NewDashboard(app).GetTeacherSummary(context.Background(), openapi.GetTeacherSummaryRequestObject{}); !errors.Is(err, httpx.ErrNotImplemented) {
		t.Fatalf("missing port=%v", err)
	}
}

func TestTheSummaryCarriesWhatIsSharedWithTheCaller(t *testing.T) {
	principal := principals()["a Teacher"]
	app := &application.Application{Queries: application.Queries{Nav: cqrs.HandlerFunc[query.Nav, domain.Nav](func(context.Context, query.Nav) (domain.Nav, error) {
		return domain.Nav{UnreadNotifications: 1, SharedWithMe: 3, UnreadMessages: 100}, nil
	})}}
	got, err := dashboardhttp.NewDashboard(app).GetTeacherSummary(gated(t, principal, "GET /teacher/summary"), openapi.GetTeacherSummaryRequestObject{})
	if err != nil {
		t.Fatal(err)
	}
	if body, ok := got.(openapi.GetTeacherSummary200JSONResponse); !ok || body.SharedWithMe != 3 || body.UnreadNotifications != 1 || body.UnreadMessages != 100 {
		t.Fatalf("summary %+v, want sharedWithMe 3 and unreadMessages 100", got)
	}
}

// TestATodaySessionReachesTheResponseAsASessionItem maps a session of the
// day to a DashboardTodaySession with kind session, its opaque id, its class
// and its room, beside an assignment item.
func TestATodaySessionReachesTheResponseAsASessionItem(t *testing.T) {
	at := time.Date(2026, 11, 23, 11, 0, 0, 0, time.FixedZone("ICT", 7*60*60))
	room := "P.204"
	class := principals()["a Teacher"].UserID
	app := &application.Application{Queries: application.Queries{Summary: cqrs.HandlerFunc[query.Summary, domain.Summary](func(context.Context, query.Summary) (domain.Summary, error) {
		return domain.Summary{Home: domain.Home{Today: []domain.Today{
			{Kind: "opens", At: at, AssignmentID: class, Title: "paper"},
			{Kind: domain.TodaySessionKind, At: at, Session: &domain.TodaySession{ID: "019535d9-3df7-79fb-b466-fa907fa17f9e.20261123", ClassID: class, ClassName: "IELTS A", Room: &room}},
		}}}, nil
	})}}
	resp, err := dashboardhttp.NewDashboard(app).GetDashboard(context.Background(), openapi.GetDashboardRequestObject{})
	if err != nil {
		t.Fatal(err)
	}
	today := resp.(openapi.GetDashboard200JSONResponse).Today
	if len(today) != 2 {
		t.Fatalf("today=%+v", today)
	}
	if kind, err := today[1].Discriminator(); err != nil || kind != "session" {
		t.Fatalf("the session's kind=%q (%v)", kind, err)
	}
	session, err := today[1].AsDashboardTodaySession()
	if err != nil {
		t.Fatal(err)
	}
	if session.SessionId != "019535d9-3df7-79fb-b466-fa907fa17f9e.20261123" || session.ClassId.String() != class || session.ClassName != "IELTS A" ||
		session.Room == nil || *session.Room != room || session.At.Location() != time.UTC || !session.At.Equal(at) {
		t.Errorf("the session reads %+v", session)
	}
	if kind, _ := today[0].Discriminator(); kind != "opens" {
		t.Errorf("the assignment's kind=%q", kind)
	}
}
