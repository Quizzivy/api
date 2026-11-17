//go:build integration

package repositories_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"quizzivy/internal/modules/notifications/domain"
	"quizzivy/internal/platform/email"
)

// aiMonth is a month no other test's rows are in, so that the rolled-back
// world may give it a pool.
var aiMonth = time.Date(2089, 3, 1, 0, 0, 0, 0, time.UTC)

func (w *dueWorld) aiPeriod() {
	w.t.Helper()
	w.exec(`INSERT INTO app.ai_credit_periods (month, pool_credits) VALUES ($1::date, 1000000) ON CONFLICT (month) DO NOTHING`, aiMonth.Format(time.DateOnly))
}

// creditManager is a staff member of a role of its own that holds only
// ai.credits.manage.
func (w *dueWorld) creditManager() string {
	w.t.Helper()
	role := w.id(`INSERT INTO app.roles (name, icon, color) VALUES ($1, 'user', 'gray') RETURNING id::text`, "Tín dụng "+uuid.NewString())
	w.exec(`INSERT INTO app.role_permissions (role_id, permission_key) VALUES ($1, 'ai.credits.manage')`, role)
	return w.id(`INSERT INTO app.users (email, full_name, role_id) VALUES ($1, 'Người duyệt', $2) RETURNING id::text`,
		uuid.NewString()+"@example.test", role)
}

func (w *dueWorld) creditRequest(teacher string) string {
	w.t.Helper()
	return w.id(`INSERT INTO app.ai_credit_requests (user_id, month, amount_credits, reason)
		VALUES ($1, $2::date, 5000, 'Ba lớp thi cuối kỳ') RETURNING id::text`, teacher, aiMonth.Format(time.DateOnly))
}

func warningNotice(userID string) domain.Notice {
	return domain.Notice{UserID: userID, Kind: domain.AIAllowanceWarning, Params: domain.AllowanceWarning{Used: 900, Allowance: 1_000},
		DedupeKey: domain.AllowanceWarningKey(aiMonth.Format(time.DateOnly)), Merge: domain.Replace}
}

func requestNotice(userID, requestID string) domain.Notice {
	return domain.Notice{UserID: userID, Kind: domain.AICreditRequest, Params: domain.CreditRequest{TeacherName: "Thành viên", Credits: 5_000},
		DedupeKey: domain.CreditRequestKey(requestID), Merge: domain.Replace}
}

func decisionNotice(userID, requestID string) domain.Notice {
	return domain.Notice{UserID: userID, Kind: domain.AICreditRequestDecided, Params: domain.CreditRequestDecided{Credits: 5_000, Decision: domain.DecisionApproved},
		DedupeKey: domain.CreditDecisionKey(requestID), Merge: domain.Replace}
}

// TestEachAIKindEnqueuesOneEmailOfIdsAlone is T-R7.23's enqueueing: the
// warning, the request and the decision each write one row in R5's outbox
// for their recipient, holding the month or the request's id and nothing the
// email says, however often Notify is called with the same notice; the
// request writes one per manager, and a disabled one gets none. No switch
// governs them, so a teacher who turned every email off still gets them.
func TestEachAIKindEnqueuesOneEmailOfIdsAlone(t *testing.T) {
	w := newDueWorld(t)
	teacher, admin, manager, gone := w.user("teacher"), w.user("admin"), w.creditManager(), w.creditManager()
	w.disabled(gone)
	for _, event := range []domain.Event{domain.EventAttemptSubmitted, domain.EventAttemptFlagged, domain.EventAssignmentClosing, domain.EventMessageReceived, domain.EventSummaryWeekly} {
		w.emailSwitch(teacher, event, false, false)
	}
	request := uuid.NewString()

	for range 2 {
		w.mail(warningNotice(teacher), w.now)
		w.mail(decisionNotice(teacher, request), w.now)
		for _, m := range []string{admin, manager, gone} {
			w.mail(requestNotice(m, request), w.now)
		}
	}

	params := func(q queued) map[string]any {
		t.Helper()
		var out map[string]any
		if err := json.Unmarshal([]byte(q.params), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	got := w.outbox(teacher)
	if len(got) != 2 || got[0].template != string(email.AIAllowanceWarning) || got[1].template != string(email.AICreditRequestDecided) {
		t.Fatalf("the teacher has %+v, want one warning and one decision", got)
	}
	if p := params(got[0]); len(p) != 1 || p["month"] != "2089-03-01T00:00:00Z" {
		t.Errorf("the warning's row holds %v, want its month alone", p)
	}
	if p := params(got[1]); len(p) != 1 || p["requestId"] != request {
		t.Errorf("the decision's row holds %v, want the request's id alone", p)
	}
	for _, m := range []string{admin, manager} {
		rows := w.outbox(m)
		if len(rows) != 1 || rows[0].template != string(email.AICreditRequest) {
			t.Errorf("manager %s has %+v, want one request email", m, rows)
			continue
		}
		if p := params(rows[0]); len(p) != 1 || p["requestId"] != request {
			t.Errorf("the request's row holds %v, want its id alone", p)
		}
		if rows[0].deliverAfter.After(time.Now()) {
			t.Errorf("the request email is due at %v, want at once", rows[0].deliverAfter)
		}
	}
	if rows := w.outbox(gone); len(rows) != 0 {
		t.Errorf("a disabled manager has %+v", rows)
	}

	off := w.mailer(func(event string) bool { return event != "ai.credit_request" })
	later := uuid.NewString()
	if err := off.Mail(context.Background(), requestNotice(manager, later), w.now); err != nil {
		t.Fatal(err)
	}
	if rows := w.outbox(manager); len(rows) != 1 {
		t.Errorf("NOTIFY_EMAIL_EVENTS without ai.credit_request still queued %+v", rows)
	}
}

// TestTheAIPreparersDropWhatIsNoLongerTrue fails when an AI email is still
// sent after what it says stopped being so: a teacher whose allowance was
// raised past the warning share, a request another manager decided first,
// a recipient whose role lost ai.credits.manage, a decision read by anyone
// but the teacher who asked.
func TestTheAIPreparersDropWhatIsNoLongerTrue(t *testing.T) {
	w := newDueWorld(t)
	ctx := context.Background()
	store := w.mailStore()
	w.aiPeriod()
	teacher := w.user("teacher")
	var percent int
	if err := w.tx.QueryRow(ctx, `SELECT warn_percent FROM app.ai_settings`).Scan(&percent); err != nil {
		t.Fatal(err)
	}
	used := int64(percent) * 10
	w.exec(`INSERT INTO app.ai_teacher_credits (month, user_id, allowance_credits, used_credits, warned_at)
		VALUES ($1::date, $2, 1000, $3, now())`, aiMonth.Format(time.DateOnly), teacher, used)
	warning := email.AIAllowanceWarningParams{Month: aiMonth}
	got, ok, err := store.AllowanceWarning(ctx, teacher, warning, w.now)
	if err != nil || !ok || got.Used != email.Credits(used) || got.Allowance != 1_000 || !got.Month.Equal(aiMonth) {
		t.Errorf("a teacher at the warning share reads %+v (%v %v)", got, ok, err)
	}
	if _, ok, _ := store.AllowanceWarning(ctx, w.user("teacher"), warning, w.now); ok {
		t.Error("a teacher with no month's row is sent the warning")
	}
	w.exec(`UPDATE app.ai_teacher_credits SET allowance_credits = 6000, granted_credits = 5000 WHERE user_id = $1`, teacher)
	if _, ok, _ := store.AllowanceWarning(ctx, teacher, warning, w.now); ok {
		t.Error("a teacher whose allowance was raised past the warning share is still warned")
	}

	request := w.creditRequest(teacher)
	ask := email.AICreditRequestParams{RequestID: request}
	admin, manager, plain := w.user("admin"), w.creditManager(), w.user("teacher")
	for name, user := range map[string]string{"the Admin": admin, "a role holding ai.credits.manage": manager} {
		if got, ok, err := store.CreditRequest(ctx, user, ask, w.now); err != nil || !ok || got.TeacherName != "Thành viên" || got.Credits != 5_000 {
			t.Errorf("%s reads the request as %+v (%v %v)", name, got, ok, err)
		}
	}
	if _, ok, _ := store.CreditRequest(ctx, plain, ask, w.now); ok {
		t.Error("a teacher who does not manage credits is sent the request")
	}
	w.exec(`DELETE FROM app.role_permissions WHERE role_id = (SELECT role_id FROM app.users WHERE id = $1)`, manager)
	if _, ok, _ := store.CreditRequest(ctx, manager, ask, w.now); ok {
		t.Error("a manager whose role lost ai.credits.manage is still sent the request")
	}

	decision := email.AICreditRequestDecidedParams{RequestID: request}
	if _, ok, _ := store.CreditDecision(ctx, teacher, decision, w.now); ok {
		t.Error("a request still waiting is sent as decided")
	}
	w.exec(`UPDATE app.ai_credit_requests SET status = 'declined', decided_by = $2, decided_at = now() WHERE id = $1`, request, admin)
	if _, ok, _ := store.CreditRequest(ctx, admin, ask, w.now); ok {
		t.Error("a request decided since is still sent to its managers")
	}
	got2, ok, err := store.CreditDecision(ctx, teacher, decision, w.now)
	if err != nil || !ok || got2.Decision != email.CreditsDeclined || got2.Credits != 5_000 || !got2.Month.Equal(aiMonth) {
		t.Errorf("the teacher's decision reads %+v (%v %v)", got2, ok, err)
	}
	if _, ok, _ := store.CreditDecision(ctx, admin, decision, w.now); ok {
		t.Error("someone other than the teacher who asked is sent the decision")
	}
}
