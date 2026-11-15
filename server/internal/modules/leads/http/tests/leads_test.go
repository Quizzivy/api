package http_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"quizzivy/gen/openapi"
	"quizzivy/internal/modules/leads/application"
	"quizzivy/internal/modules/leads/domain"
	leadshttp "quizzivy/internal/modules/leads/http"
	"quizzivy/internal/platform/httpapi"
	"quizzivy/internal/platform/httpx"
	"quizzivy/internal/shared/paging"
)

var now = time.Date(2026, 11, 15, 9, 30, 0, 0, time.UTC)

const (
	leadID     = "019535d9-3df7-79fb-b466-fa907fa17f01"
	missingID  = "019535d9-3df7-79fb-b466-fa907fa17f02"
	actorID    = "019535d9-3df7-79fb-b466-fa907fa17f9e"
	typedName  = "Trần Thị Mai"
	typedPhone = "0903 123 456"
)

type memory struct {
	stored  []domain.Submit
	lead    domain.Lead
	updates []domain.Update
	deleted []string
	lists   []domain.ListQuery
}

func (m *memory) Submit(_ context.Context, in domain.Submit) (domain.Drop, error) {
	recent := 0
	for _, s := range m.stored {
		if s.Submission.PhoneDigits == in.Submission.PhoneDigits {
			recent++
		}
	}
	verdict := in.Submission.Verdict(recent)
	if verdict == domain.Stored {
		m.stored = append(m.stored, in)
	}
	return verdict, nil
}

func (m *memory) List(_ context.Context, q domain.ListQuery) ([]domain.Lead, paging.Page, error) {
	m.lists = append(m.lists, q)
	return []domain.Lead{m.lead}, paging.Page{Number: 3, Size: 10, Total: 21}, nil
}

func (m *memory) Get(_ context.Context, id string) (domain.Lead, error) {
	if id != m.lead.ID {
		return domain.Lead{}, domain.ErrNotFound
	}
	return m.lead, nil
}

func (m *memory) Update(_ context.Context, in domain.Update) (domain.Lead, error) {
	if in.ID != m.lead.ID {
		return domain.Lead{}, domain.ErrNotFound
	}
	m.updates = append(m.updates, in)
	return m.lead, nil
}

func (m *memory) Delete(_ context.Context, in domain.Delete) error {
	if in.ID != m.lead.ID {
		return domain.ErrNotFound
	}
	m.deleted = append(m.deleted, in.ID)
	return nil
}

func newRepo() *memory {
	return &memory{lead: domain.Lead{
		ID: leadID, FullName: typedName, PhoneDigits: "0903123456", Interests: []string{"ielts", "kids"},
		CourseFor: "child", CallTime: "evening", Locale: "vi", PrivacyNoticeVersion: "2026-11",
		ConsentedAt: now, Status: domain.StatusNew, CreatedAt: now,
	}}
}

func handler(repo *memory) leadshttp.Leads {
	app := application.New(repo, slog.New(slog.DiscardHandler))
	app.SetClock(func() time.Time { return now })
	return leadshttp.NewLeads(app)
}

func contextAs(t *testing.T, signedIn bool, acceptLanguage string) context.Context {
	t.Helper()
	var ctx context.Context
	verify := func(string) (httpx.Principal, error) { return httpx.Principal{UserID: actorID}, nil }
	open := map[string]struct{}{}
	if !signedIn {
		open["POST /public/leads"] = struct{}{}
	}
	h := httpx.WithRequestMeta(func(*http.Request) string { return "203.0.113.9" })(
		httpx.RequireAuth(open, verify)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { ctx = r.Context() })))
	req := httptest.NewRequest(http.MethodPost, "/public/leads", nil)
	req.Pattern = "POST /public/leads"
	if signedIn {
		req.Header.Set("Authorization", "Bearer token")
	}
	if acceptLanguage != "" {
		req.Header.Set("Accept-Language", acceptLanguage)
	}
	h.ServeHTTP(httptest.NewRecorder(), req)
	if ctx == nil {
		t.Fatal("the request did not reach the handler")
	}
	return ctx
}

func input() openapi.LeadInput {
	return openapi.LeadInput{
		FullName: typedName, Phone: typedPhone, Interests: []openapi.LeadInterest{openapi.Ielts},
		CourseFor: openapi.Self, CallTime: openapi.LeadCallTimeAny, Locale: openapi.LeadInputLocaleVi,
		Consent: openapi.LeadInputConsentTrue, PrivacyNoticeVersion: "2026-11",
	}
}

func uuidOf(s string) openapi.Uuid { return httpapi.ParseUUID(s) }

type written struct {
	status int
	header http.Header
	body   string
}

func write(t *testing.T, visit func(http.ResponseWriter) error) written {
	t.Helper()
	rec := httptest.NewRecorder()
	if err := visit(rec); err != nil {
		t.Fatalf("writing the response: %v", err)
	}
	return written{rec.Code, rec.Header(), rec.Body.String()}
}

func submit(t *testing.T, h leadshttp.Leads, in openapi.LeadInput, language string) written {
	t.Helper()
	res, err := h.SubmitLead(contextAs(t, false, language), openapi.SubmitLeadRequestObject{Body: &in})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	return write(t, res.VisitSubmitLeadResponse)
}

// TestEveryAcceptedSubmissionGetsTheSameAnswerStoredOrNot fails when the
// transport tells a stored request from a honeypot or a repeat by its status,
// its body or a header, which would say that a number was seen before or that
// website was read. The application's tests cover what is logged; no other
// test compares the three answers.
func TestEveryAcceptedSubmissionGetsTheSameAnswerStoredOrNot(t *testing.T) {
	repo := newRepo()
	h := handler(repo)
	first := submit(t, h, input(), "")
	second := submit(t, h, input(), "")
	repeat := submit(t, h, input(), "")
	trapped := input()
	site := "https://spam.example"
	trapped.Phone, trapped.Website = "0911 222 333", &site
	honeypot := submit(t, h, trapped, "")

	if len(repo.stored) != 2 {
		t.Fatalf("%d requests stored, want the first two", len(repo.stored))
	}
	if first.status != http.StatusAccepted || strings.TrimSpace(first.body) != `{"received":true}` {
		t.Fatalf("a stored request answered %d %s, want 202 {\"received\":true}", first.status, first.body)
	}
	for name, got := range map[string]written{"the second request": second, "a repeat": repeat, "a honeypot": honeypot} {
		if got.status != first.status || got.body != first.body || !slices.Equal(headerNames(got.header), headerNames(first.header)) {
			t.Errorf("%s answered %d %q with headers %v; a stored one answers %d %q with %v",
				name, got.status, got.body, headerNames(got.header), first.status, first.body, headerNames(first.header))
		}
	}
}

func headerNames(h http.Header) []string {
	names := make([]string, 0, len(h))
	for name, values := range h {
		names = append(names, name+"="+strings.Join(values, ","))
	}
	sort.Strings(names)
	return names
}

// TestARefusedSubmissionNamesItsFieldInTheReadersLanguageAndRepeatsNoValue
// fails when the 400 of submitLead stops being VALIDATION_FAILED keyed by the
// contract's field name, ignores Accept-Language, or echoes the name or the
// number back. The domain test covers which values are refused.
func TestARefusedSubmissionNamesItsFieldInTheReadersLanguageAndRepeatsNoValue(t *testing.T) {
	bad := input()
	bad.Phone = "12 34 56"
	bad.FullName = "Mai\u200bLan"
	for language, want := range map[string]string{"": "Số điện thoại phải có từ 9 đến 15 chữ số.", "en": "The phone number must have 9 to 15 digits."} {
		repo := newRepo()
		got := submit(t, handler(repo), bad, language)
		var body struct {
			Error struct {
				Code    string            `json:"code"`
				Details map[string]string `json:"details"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(got.body), &body); err != nil {
			t.Fatalf("body %s: %v", got.body, err)
		}
		if got.status != http.StatusBadRequest || body.Error.Code != "VALIDATION_FAILED" {
			t.Errorf("%q: %d %s, want 400 VALIDATION_FAILED", language, got.status, got.body)
		}
		if body.Error.Details["phone"] != want || body.Error.Details["fullName"] == "" || len(body.Error.Details) != 2 {
			t.Errorf("%q: details = %v, want phone and fullName named, phone as %q", language, body.Error.Details, want)
		}
		for _, typed := range []string{"12 34 56", "123456", "Mai", "Lan"} {
			if strings.Contains(got.body, typed) {
				t.Errorf("%q: the refusal repeats %q: %s", language, typed, got.body)
			}
		}
		if len(repo.stored) != 0 {
			t.Errorf("%q: a refused request was stored", language)
		}
	}
}

// TestALeadIsServedWithEveryContractKeyAndNeverCached fails when a read of a
// request loses its Cache-Control: private, no-store, drops a key the
// contract requires (an unhandled request must carry note, handledBy and
// handledAt as nulls), or mislabels the list's page.
func TestALeadIsServedWithEveryContractKeyAndNeverCached(t *testing.T) {
	repo := newRepo()
	h := handler(repo)
	ctx := contextAs(t, true, "")
	one, err := h.GetLead(ctx, openapi.GetLeadRequestObject{Id: uuidOf(leadID)})
	if err != nil {
		t.Fatal(err)
	}
	got := write(t, one.VisitGetLeadResponse)
	if got.status != http.StatusOK || got.header.Get("Cache-Control") != "private, no-store" {
		t.Errorf("GET one: %d with Cache-Control %q", got.status, got.header.Get("Cache-Control"))
	}
	var lead map[string]json.RawMessage
	if err := json.Unmarshal([]byte(got.body), &lead); err != nil {
		t.Fatal(err)
	}
	want := []string{"callTime", "consentedAt", "courseFor", "createdAt", "fullName", "handledAt", "handledBy", "id", "interests", "locale", "note", "phone", "privacyNoticeVersion", "status"}
	keys := make([]string, 0, len(lead))
	for key := range lead {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if !slices.Equal(keys, want) {
		t.Errorf("keys = %v, want %v", keys, want)
	}
	for key, value := range map[string]string{
		"note": "null", "handledBy": "null", "handledAt": "null", "phone": `"0903123456"`, "status": `"new"`,
		"interests": `["ielts","kids"]`, "consentedAt": `"2026-11-15T09:30:00Z"`,
	} {
		if string(lead[key]) != value {
			t.Errorf("%s = %s, want %s", key, lead[key], value)
		}
	}

	handledAt := now.Add(time.Hour)
	repo.lead.Status, repo.lead.Note, repo.lead.HandledAt = domain.StatusContacted, "Đã gọi", &handledAt
	repo.lead.HandledBy = &domain.Handler{ID: actorID, Name: "Cô Thương"}
	status, q, page, limit := openapi.LeadStatusContacted, " mai ", 3, openapi.ListLeadsParamsLimitN10
	list, err := h.ListLeads(ctx, openapi.ListLeadsRequestObject{Params: openapi.ListLeadsParams{Status: &status, Q: &q, Page: &page, Limit: &limit}})
	if err != nil {
		t.Fatal(err)
	}
	listed := write(t, list.VisitListLeadsResponse)
	if listed.header.Get("Cache-Control") != "private, no-store" {
		t.Errorf("GET list: Cache-Control %q", listed.header.Get("Cache-Control"))
	}
	var body struct {
		Items []struct {
			Note      *string `json:"note"`
			HandledAt *string `json:"handledAt"`
			HandledBy *struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"handledBy"`
		} `json:"items"`
		Page, PageSize, Total int
	}
	if err := json.Unmarshal([]byte(listed.body), &body); err != nil {
		t.Fatal(err)
	}
	if body.Page != 3 || body.PageSize != 10 || body.Total != 21 || len(body.Items) != 1 {
		t.Fatalf("list = %s", listed.body)
	}
	item := body.Items[0]
	if item.Note == nil || *item.Note != "Đã gọi" || item.HandledAt == nil || *item.HandledAt != "2026-11-15T10:30:00Z" ||
		item.HandledBy == nil || item.HandledBy.ID != actorID || item.HandledBy.Name != "Cô Thương" {
		t.Errorf("a handled request = %s", listed.body)
	}
	if len(repo.lists) != 1 || repo.lists[0] != (domain.ListQuery{Status: domain.StatusContacted, Q: "mai", Page: 3, Limit: 10}) {
		t.Errorf("the store was asked %+v", repo.lists)
	}
}

func patch(t *testing.T, h leadshttp.Leads, id, raw string) written {
	t.Helper()
	var body openapi.LeadUpdate
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		t.Fatalf("body %s: %v", raw, err)
	}
	res, err := h.UpdateLead(contextAs(t, true, ""), openapi.UpdateLeadRequestObject{Id: uuidOf(id), Body: &body})
	if err != nil {
		t.Fatalf("patch %s: %v", raw, err)
	}
	return write(t, res.VisitUpdateLeadResponse)
}

// TestAPatchTellsNoNoteFromANullOneAndFromAnEmptyOne fails when a body with a
// status alone wipes the note, when "note": null is read as no note at all and
// so keeps what is stored, when an empty note is, or when the caller is not
// carried to the store. The contract's three cases reach the handler as raw
// JSON, and this is the only test that decodes them as the router does. The
// application test covers the tidying of the note.
func TestAPatchTellsNoNoteFromANullOneAndFromAnEmptyOne(t *testing.T) {
	repo := newRepo()
	h := handler(repo)
	bodies := []string{`{"status":"contacted"}`, `{"note":null}`, `{"note":""}`, `{"note":"  Đã gọi  ","status":"closed"}`, `{"status":"new","note":null}`}
	for _, raw := range bodies {
		if got := patch(t, h, leadID, raw); got.status != http.StatusOK {
			t.Fatalf("%s answered %d %s", raw, got.status, got.body)
		}
	}
	if len(repo.updates) != len(bodies) {
		t.Fatalf("%d updates reached the store, want %d", len(repo.updates), len(bodies))
	}
	statusOnly, noteNull, noteEmpty, both, statusAndNull := repo.updates[0], repo.updates[1], repo.updates[2], repo.updates[3], repo.updates[4]
	if statusOnly.Change.NoteSet || statusOnly.Change.Status == nil || *statusOnly.Change.Status != domain.StatusContacted {
		t.Errorf("a status alone = %+v, want the note kept", statusOnly.Change)
	}
	if !noteNull.Change.NoteSet || noteNull.Change.Note != "" || noteNull.Change.Status != nil {
		t.Errorf("a null note = %+v, want the note removed and the status kept", noteNull.Change)
	}
	if !noteEmpty.Change.NoteSet || noteEmpty.Change.Note != "" || noteEmpty.Change.Status != nil {
		t.Errorf("an empty note = %+v, want the note removed and the status kept", noteEmpty.Change)
	}
	if !both.Change.NoteSet || both.Change.Note != "Đã gọi" || *both.Change.Status != domain.StatusClosed {
		t.Errorf("a note and a status = %+v", both.Change)
	}
	if !statusAndNull.Change.NoteSet || statusAndNull.Change.Note != "" || *statusAndNull.Change.Status != domain.StatusNew {
		t.Errorf("a status and a null note = %+v, want both carried", statusAndNull.Change)
	}
	if both.ActorID != actorID || both.IP != "203.0.113.9" || !both.Now.Equal(now) {
		t.Errorf("the update names %q from %q at %v", both.ActorID, both.IP, both.Now)
	}

	wrong := openapi.LeadUpdate{Note: json.RawMessage(`7`)}
	res, err := h.UpdateLead(contextAs(t, true, "en"), openapi.UpdateLeadRequestObject{Id: uuidOf(leadID), Body: &wrong})
	if err != nil {
		t.Fatal(err)
	}
	if got := write(t, res.VisitUpdateLeadResponse); got.status != http.StatusBadRequest || !strings.Contains(got.body, `"note":"The note must be text."`) || len(repo.updates) != len(bodies) {
		t.Errorf("a note that is no text: %d %s with %d updates at the store, want 400 naming note and nothing stored", got.status, got.body, len(repo.updates))
	}
}

// TestARequestNobodyHasAnswers404OnEveryOperationThatNamesOne fails when a
// missing request surfaces as a 500 or an empty 200 on the read, the change or
// the removal.
func TestARequestNobodyHasAnswers404OnEveryOperationThatNamesOne(t *testing.T) {
	repo := newRepo()
	h := handler(repo)
	ctx := contextAs(t, true, "en")
	one, err := h.GetLead(ctx, openapi.GetLeadRequestObject{Id: uuidOf(missingID)})
	if err != nil {
		t.Fatal(err)
	}
	gone, err := h.DeleteLead(ctx, openapi.DeleteLeadRequestObject{Id: uuidOf(missingID)})
	if err != nil {
		t.Fatal(err)
	}
	for name, got := range map[string]written{
		"GET":    write(t, one.VisitGetLeadResponse),
		"PATCH":  patch(t, h, missingID, `{"status":"closed"}`),
		"DELETE": write(t, gone.VisitDeleteLeadResponse),
	} {
		if got.status != http.StatusNotFound || !strings.Contains(got.body, `"NOT_FOUND"`) {
			t.Errorf("%s of a missing request: %d %s", name, got.status, got.body)
		}
	}
	removed, err := h.DeleteLead(ctx, openapi.DeleteLeadRequestObject{Id: uuidOf(leadID)})
	if err != nil {
		t.Fatal(err)
	}
	if got := write(t, removed.VisitDeleteLeadResponse); got.status != http.StatusNoContent || got.body != "" || !slices.Equal(repo.deleted, []string{leadID}) {
		t.Errorf("DELETE of a stored request: %d %q, the store removed %v", got.status, got.body, repo.deleted)
	}
}

// TestWithoutAnApplicationEveryOperationIsNotImplemented fails when a
// transport built without its application dereferences it: the router's own
// tests build the module that way.
func TestWithoutAnApplicationEveryOperationIsNotImplemented(t *testing.T) {
	h := leadshttp.NewLeads(nil)
	ctx := contextAs(t, true, "")
	in := input()
	var update openapi.LeadUpdate
	calls := map[string]func() error{
		"submit": func() error { _, err := h.SubmitLead(ctx, openapi.SubmitLeadRequestObject{Body: &in}); return err },
		"list":   func() error { _, err := h.ListLeads(ctx, openapi.ListLeadsRequestObject{}); return err },
		"get":    func() error { _, err := h.GetLead(ctx, openapi.GetLeadRequestObject{Id: uuidOf(leadID)}); return err },
		"update": func() error {
			_, err := h.UpdateLead(ctx, openapi.UpdateLeadRequestObject{Id: uuidOf(leadID), Body: &update})
			return err
		},
		"delete": func() error {
			_, err := h.DeleteLead(ctx, openapi.DeleteLeadRequestObject{Id: uuidOf(leadID)})
			return err
		},
	}
	for name, call := range calls {
		if err := call(); !errors.Is(err, httpx.ErrNotImplemented) {
			t.Errorf("%s without an application: %v, want ErrNotImplemented", name, err)
		}
	}
}
