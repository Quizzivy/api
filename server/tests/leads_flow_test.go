//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"

	"quizzivy/internal/platform/config"
)

const (
	landingOrigin   = "https://quizzivy.com"
	clientIPHeader  = "Fly-Client-IP"
	leadVisitorName = "Trần Thị Mai"
)

var leadVisitors atomic.Int64

func bootForLeads(t *testing.T) *world {
	t.Helper()
	w := boot(t, func(c *config.Config) {
		c.LandingOrigin = landingOrigin
		c.ClientIPHeader = clientIPHeader
	})
	t.Cleanup(func() {
		ctx := context.Background()
		for _, stmt := range []string{
			`UPDATE app.organization_settings SET lead_recipients = '{}'`,
			`DELETE FROM app.email_outbox WHERE template = 'lead_received'`,
			`DELETE FROM app.leads`,
		} {
			if _, err := w.pool.Exec(ctx, stmt); err != nil {
				t.Errorf("cleanup %q: %v", stmt, err)
			}
		}
	})
	return w
}

func (w *world) recipientsForLeads(addresses ...string) {
	w.t.Helper()
	if _, err := w.pool.Exec(context.Background(), `UPDATE app.organization_settings SET lead_recipients = $1`, addresses); err != nil {
		w.t.Fatal(err)
	}
}

type formAnswer struct {
	status int
	header http.Header
	body   string
}

func (w *world) sendForm(raw []byte, origin string) formAnswer {
	w.t.Helper()
	req, err := http.NewRequest(http.MethodPost, w.server.URL+"/public/leads", bytes.NewReader(raw))
	if err != nil {
		w.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(clientIPHeader, fmt.Sprintf("198.51.100.%d", leadVisitors.Add(1)%250+1))
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		w.t.Fatalf("POST /public/leads: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	return formAnswer{resp.StatusCode, resp.Header, strings.TrimSpace(string(body))}
}

func leadForm(phone string, change func(map[string]any)) []byte {
	form := map[string]any{
		"fullName": "  Trần   Thị Mai ", "phone": phone, "interests": []string{"ielts", "kids"}, "courseFor": "child",
		"callTime": "evening", "locale": "vi", "consent": true, "privacyNoticeVersion": "2026-11",
	}
	if change != nil {
		change(form)
	}
	raw, _ := json.Marshal(form)
	return raw
}

func (w *world) leadsOf(digits string) int {
	w.t.Helper()
	var n int
	if err := w.pool.QueryRow(context.Background(), `SELECT count(*) FROM app.leads WHERE phone_digits = $1`, digits).Scan(&n); err != nil {
		w.t.Fatal(err)
	}
	return n
}

// TestAConsultationRequestTravelsFromTheLandingFormToTheAdminWhoHandlesAndRemovesIt
// fails when any link of the whole path breaks in the assembled application:
// the form's CORS answer for the landing origin, the stored request, the
// notice queued for the settings' recipient without the number or the name,
// the nav count, the Admin's search, the change that names who handled it,
// the removal, and the audit entries that outlive it. Each piece has its own
// narrower test; none of them runs the real router over the real database.
func TestAConsultationRequestTravelsFromTheLandingFormToTheAdminWhoHandlesAndRemovesIt(t *testing.T) {
	w := bootForLeads(t)
	ctx := context.Background()
	email, password := w.createStaff("admin")
	admin := w.signedIn(email, password)
	adminID := w.userID(email)
	recipient := "tu-van-" + nonce(t) + "@example.com"

	settings := admin.must(http.StatusOK, http.MethodGet, "/admin/settings", nil)
	if got, ok := settings["leadRecipients"].([]any); !ok || len(got) != 0 {
		t.Fatalf("leadRecipients before any save = %v, want an empty list", settings["leadRecipients"])
	}
	saved := admin.must(http.StatusOK, http.MethodPut, "/admin/settings", map[string]any{
		"organization": settings["organization"], "signIn": settings["signIn"], "retention": settings["retention"],
		"expectedRevision": settings["revision"], "leadRecipients": []string{strings.ToUpper(recipient[:6]) + recipient[6:]},
	})
	if got := saved["leadRecipients"].([]any); len(got) != 1 || got[0] != recipient {
		t.Fatalf("saved leadRecipients = %v, want %q lower-case", got, recipient)
	}
	before := admin.must(http.StatusOK, http.MethodGet, "/admin/summary", nil)["newLeads"].(float64)

	const typed, digits = "(090) 312-3456", "0903123456"
	sent := w.sendForm(leadForm(typed, nil), landingOrigin)
	if sent.status != http.StatusAccepted || sent.body != `{"received":true}` {
		t.Fatalf("the form answered %d %s, want 202 {\"received\":true}", sent.status, sent.body)
	}
	if sent.header.Get("Access-Control-Allow-Origin") != landingOrigin || sent.header.Get("Access-Control-Allow-Credentials") != "" {
		t.Errorf("the form's answer to the landing origin: Allow-Origin %q, Allow-Credentials %q",
			sent.header.Get("Access-Control-Allow-Origin"), sent.header.Get("Access-Control-Allow-Credentials"))
	}

	if after := admin.must(http.StatusOK, http.MethodGet, "/admin/summary", nil)["newLeads"].(float64); after != before+1 {
		t.Errorf("newLeads went from %v to %v, want one more", before, after)
	}
	listed := admin.must(http.StatusOK, http.MethodGet, "/admin/leads?status=new&q="+url.QueryEscape("tran thi MAI"), nil)
	items := listed["items"].([]any)
	if len(items) != 1 || listed["total"] != float64(1) {
		t.Fatalf("the Admin's search by name without accents found %v", listed)
	}
	lead := items[0].(map[string]any)
	leadID := id(lead)
	if lead["fullName"] != leadVisitorName || lead["phone"] != digits || lead["status"] != "new" || lead["note"] != nil ||
		lead["handledBy"] != nil || lead["handledAt"] != nil || lead["privacyNoticeVersion"] != "2026-11" || lead["consentedAt"] != lead["createdAt"] {
		t.Errorf("the stored request = %v", lead)
	}
	byNumber := admin.must(http.StatusOK, http.MethodGet, "/admin/leads?q="+url.QueryEscape("090 312 3456"), nil)
	if got := byNumber["items"].([]any); len(got) != 1 || id(got[0].(map[string]any)) != leadID {
		t.Errorf("the Admin's search by the number as typed found %v", byNumber)
	}

	var to, template, payload string
	var queued int
	if err := w.pool.QueryRow(ctx, `
		SELECT count(*), min(to_address), min(template), min(row_to_json(o)::text)
		  FROM app.email_outbox o WHERE o.params->>'leadId' = $1`, leadID).Scan(&queued, &to, &template, &payload); err != nil {
		t.Fatal(err)
	}
	if queued != 1 || to != recipient || template != "lead_received" {
		t.Fatalf("%d messages queued to %q as %q, want one lead_received to %q", queued, to, template, recipient)
	}
	for what, held := range map[string]string{"the digits": digits, "the number as typed": typed, "the full name": leadVisitorName, "the family name": "Trần"} {
		if strings.Contains(payload, held) {
			t.Errorf("the queued message holds %s: %s", what, payload)
		}
	}
	if !strings.Contains(payload, `"givenName": "Mai"`) && !strings.Contains(payload, `"givenName":"Mai"`) {
		t.Errorf("the queued message does not name Mai: %s", payload)
	}

	changed := admin.must(http.StatusOK, http.MethodPatch, "/admin/leads/"+leadID, map[string]any{"status": "contacted", "note": "  Hẹn gọi lại thứ Bảy  "})
	handler, _ := changed["handledBy"].(map[string]any)
	if changed["status"] != "contacted" || changed["note"] != "Hẹn gọi lại thứ Bảy" || handler["id"] != adminID || handler["name"] != "Cô Thương" || changed["handledAt"] == nil {
		t.Errorf("after the change = %v", changed)
	}
	again := admin.must(http.StatusOK, http.MethodPatch, "/admin/leads/"+leadID, map[string]any{"status": "contacted"})
	if again["handledAt"] != changed["handledAt"] || again["note"] != changed["note"] {
		t.Errorf("a change that restates the status = %v, want the request as it stood", again)
	}
	if after := admin.must(http.StatusOK, http.MethodGet, "/admin/summary", nil)["newLeads"].(float64); after != before {
		t.Errorf("newLeads is %v once the request is handled, want %v", after, before)
	}

	admin.must(http.StatusNoContent, http.MethodDelete, "/admin/leads/"+leadID, nil)
	if status, body := admin.call(http.MethodGet, "/admin/leads/"+leadID, nil); status != http.StatusNotFound || refusalCode(body) != "NOT_FOUND" {
		t.Errorf("reading the removed request: %d %v, want 404 NOT_FOUND", status, body)
	}
	if status, _ := admin.call(http.MethodDelete, "/admin/leads/"+leadID, nil); status != http.StatusNotFound {
		t.Errorf("removing it again: %d, want 404", status)
	}

	rows, err := w.pool.Query(ctx, `SELECT action, row_to_json(a)::text FROM app.audit_log a WHERE entity_id = $1::uuid ORDER BY id`, leadID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var actions []string
	for rows.Next() {
		var action, whole string
		if err := rows.Scan(&action, &whole); err != nil {
			t.Fatal(err)
		}
		actions = append(actions, action)
		for what, held := range map[string]string{"the note": "Hẹn gọi lại", "the name": leadVisitorName, "the given name": "Mai", "the number": digits} {
			if strings.Contains(whole, held) {
				t.Errorf("the %s entry holds %s: %s", action, what, whole)
			}
		}
		if !strings.Contains(whole, adminID) {
			t.Errorf("the %s entry does not name the Admin: %s", action, whole)
		}
	}
	if strings.Join(actions, ",") != "lead.updated,lead.deleted" {
		t.Errorf("audit entries = %v, want one change and one removal", actions)
	}
	log := admin.must(http.StatusOK, http.MethodGet, "/admin/audit-log?q="+url.QueryEscape("yêu cầu tư vấn"), nil)
	if found, _ := log["items"].([]any); len(found) < 2 {
		t.Errorf("the audit log's search for the action's sentence found %d entries, want the change and the removal", len(found))
	}
}

// TestANullNoteRemovesAStoredNoteAndIsAuditedWithoutIt fails when the body
// {"note": null} is decoded as a body that names no note and so keeps what
// is stored, when the removal is not audited as noteChanged alone or its
// entry holds the note, and when the same body on a request without a note
// writes anything. The handler's test decodes the three cases against a fake
// store; this one sends the bytes through the validator and the real store.
func TestANullNoteRemovesAStoredNoteAndIsAuditedWithoutIt(t *testing.T) {
	w := bootForLeads(t)
	ctx := context.Background()
	email, password := w.createStaff("admin")
	admin := w.signedIn(email, password)
	if got := w.sendForm(leadForm("0944 555 666", nil), ""); got.status != http.StatusAccepted {
		t.Fatalf("the form answered %d %s", got.status, got.body)
	}
	listed := admin.must(http.StatusOK, http.MethodGet, "/admin/leads?q=0944555666", nil)
	leadID := id(listed["items"].([]any)[0].(map[string]any))
	const note = "Hẹn gọi lại thứ Bảy, sau 18 giờ"
	audited := func() []string {
		t.Helper()
		rows, err := w.pool.Query(ctx, `SELECT coalesce(diff::text, '') FROM app.audit_log WHERE entity_id = $1::uuid ORDER BY id`, leadID)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var diffs []string
		for rows.Next() {
			var diff string
			if err := rows.Scan(&diff); err != nil {
				t.Fatal(err)
			}
			diffs = append(diffs, diff)
		}
		return diffs
	}
	updatedAt := func() string {
		t.Helper()
		var at string
		if err := w.pool.QueryRow(ctx, `SELECT updated_at::text FROM app.leads WHERE id = $1::uuid`, leadID).Scan(&at); err != nil {
			t.Fatal(err)
		}
		return at
	}

	unchanged := admin.must(http.StatusOK, http.MethodPatch, "/admin/leads/"+leadID, map[string]any{"note": nil})
	if unchanged["note"] != nil || len(audited()) != 0 {
		t.Fatalf("a null note on a request without one: note %v and %d audit entries, want nothing written", unchanged["note"], len(audited()))
	}

	if set := admin.must(http.StatusOK, http.MethodPatch, "/admin/leads/"+leadID, map[string]any{"note": note}); set["note"] != note {
		t.Fatalf("the note was stored as %v", set["note"])
	}
	kept := admin.must(http.StatusOK, http.MethodPatch, "/admin/leads/"+leadID, map[string]any{"status": "contacted"})
	if kept["note"] != note {
		t.Fatalf("a body without a note left %v, want the note kept", kept["note"])
	}

	removed := admin.must(http.StatusOK, http.MethodPatch, "/admin/leads/"+leadID, map[string]any{"note": nil})
	if removed["note"] != nil || removed["status"] != "contacted" || removed["handledAt"] != kept["handledAt"] {
		t.Errorf("after a null note = %v, want the note gone and the handling as it stood", removed)
	}
	if read := admin.must(http.StatusOK, http.MethodGet, "/admin/leads/"+leadID, nil); read["note"] != nil {
		t.Errorf("the request reads back with the note %v", read["note"])
	}
	diffs := audited()
	if len(diffs) != 3 {
		t.Fatalf("%d audit entries, want the note, the status and the removal: %v", len(diffs), diffs)
	}
	var last map[string]any
	if err := json.Unmarshal([]byte(diffs[2]), &last); err != nil || len(last) != 1 || last["noteChanged"] != true {
		t.Errorf("the removal's diff = %s, want only noteChanged: true", diffs[2])
	}
	for _, diff := range diffs {
		if strings.Contains(diff, "Hẹn gọi") || strings.Contains(diff, "18 giờ") {
			t.Errorf("an audit entry holds the note: %s", diff)
		}
	}

	stamp := updatedAt()
	again := admin.must(http.StatusOK, http.MethodPatch, "/admin/leads/"+leadID, map[string]any{"note": nil})
	if again["note"] != nil || len(audited()) != 3 || updatedAt() != stamp {
		t.Errorf("a second null note wrote something: %d audit entries, updated_at %s then %s", len(audited()), stamp, updatedAt())
	}
	if blank := admin.must(http.StatusOK, http.MethodPatch, "/admin/leads/"+leadID, map[string]any{"note": "   "}); blank["note"] != nil || len(audited()) != 3 {
		t.Errorf("a blank note on a request without one wrote something: %v, %d audit entries", blank["note"], len(audited()))
	}
}

// TestTheFormRefusesWhatTheContractForbidsAndStoresNothingForARefusalOrAHoneypot
// fails when a request without consent, with a notice version this server
// does not know, with an unknown choice or property, or over 4 KiB reaches
// the handler, and when the handler's own two refusals stop naming their
// field or repeat a value. It also fails when a honeypot is told apart from a
// stored request or is stored. The schema's rules run in the validator, which
// only the assembled router has.
func TestTheFormRefusesWhatTheContractForbidsAndStoresNothingForARefusalOrAHoneypot(t *testing.T) {
	w := bootForLeads(t)
	const typed, digits = "0911 222 333", "0911222333"
	for name, c := range map[string]struct {
		change func(map[string]any)
		status int
		field  string
	}{
		"consent false":              {func(f map[string]any) { f["consent"] = false }, http.StatusBadRequest, "consent"},
		"consent as a string":        {func(f map[string]any) { f["consent"] = "true" }, http.StatusBadRequest, "consent"},
		"no consent":                 {func(f map[string]any) { delete(f, "consent") }, http.StatusBadRequest, "consent"},
		"last month's notice":        {func(f map[string]any) { f["privacyNoticeVersion"] = "2026-10" }, http.StatusBadRequest, "privacyNoticeVersion"},
		"no notice version":          {func(f map[string]any) { delete(f, "privacyNoticeVersion") }, http.StatusBadRequest, "privacyNoticeVersion"},
		"an interest nobody offers":  {func(f map[string]any) { f["interests"] = []string{"ielts", "chess"} }, http.StatusBadRequest, "interests"},
		"an interest twice":          {func(f map[string]any) { f["interests"] = []string{"ielts", "ielts"} }, http.StatusBadRequest, "interests"},
		"a course for nobody listed": {func(f map[string]any) { f["courseFor"] = "friend" }, http.StatusBadRequest, "courseFor"},
		"a third language":           {func(f map[string]any) { f["locale"] = "fr" }, http.StatusBadRequest, "locale"},
		"letters in the number":      {func(f map[string]any) { f["phone"] = "0911 222 abc" }, http.StatusBadRequest, "phone"},
		"a number of eight digits":   {func(f map[string]any) { f["phone"] = "(09) 11-22-33" }, http.StatusBadRequest, "phone"},
		"a name of spaces":           {func(f map[string]any) { f["fullName"] = "   " }, http.StatusBadRequest, "fullName"},
		"a name with a hidden mark":  {func(f map[string]any) { f["fullName"] = "Mai\u200bLan" }, http.StatusBadRequest, "fullName"},
		"a property nobody declared": {func(f map[string]any) { f["ip"] = "203.0.113.1" }, http.StatusBadRequest, ""},
		"a body over four kibibytes": {func(f map[string]any) { f["website"] = strings.Repeat("x", 5000) }, http.StatusRequestEntityTooLarge, ""},
	} {
		got := w.sendForm(leadForm(typed, c.change), "")
		if got.status != c.status {
			t.Errorf("%s: %d %s, want %d", name, got.status, got.body, c.status)
			continue
		}
		if c.field != "" && !strings.Contains(got.body, c.field) {
			t.Errorf("%s: the refusal does not name %s: %s", name, c.field, got.body)
		}
		for _, value := range []string{"0911", "222 abc", "Mai", "Trần", "chess", "friend", "203.0.113.1"} {
			if strings.Contains(got.body, value) {
				t.Errorf("%s: the refusal repeats %q: %s", name, value, got.body)
			}
		}
	}
	if n := w.leadsOf(digits); n != 0 {
		t.Fatalf("%d requests were stored from refused forms", n)
	}

	stored := w.sendForm(leadForm(typed, nil), "")
	trapped := w.sendForm(leadForm("0922 333 444", func(f map[string]any) { f["website"] = "https://spam.example" }), "")
	if stored.status != http.StatusAccepted || trapped.status != stored.status || trapped.body != stored.body {
		t.Errorf("a stored request answered %d %s and a honeypot %d %s; the two must be the same", stored.status, stored.body, trapped.status, trapped.body)
	}
	if w.leadsOf(digits) != 1 || w.leadsOf("0922333444") != 0 {
		t.Errorf("stored %d requests for the form and %d for the honeypot, want 1 and 0", w.leadsOf(digits), w.leadsOf("0922333444"))
	}
	w.sendForm(leadForm(typed, nil), "")
	third := w.sendForm(leadForm("+84 911 222 333", nil), "")
	repeat := w.sendForm(leadForm(typed, nil), "")
	if repeat.status != http.StatusAccepted || repeat.body != stored.body || third.status != http.StatusAccepted {
		t.Errorf("a third request for the number answered %d %s, want the stored request's answer", repeat.status, repeat.body)
	}
	if n := w.leadsOf(digits); n != 2 {
		t.Errorf("%d requests stored for one number in a day, want 2", n)
	}
}

// TestWhateverFillsTheHoneypotIsAnsweredAsAStoredRequestAndNeverStored fails
// when a value of website is answered otherwise than a stored request is: a
// length the schema refuses (it had one, of 200), a shape the handler reads
// as empty, or anything that reaches the store. It also fails when an empty
// website, which a form sends for a field nobody touched, is taken for a
// filled one and the request is lost. The contract test above sends one
// short honeypot; the handler's test never passes the validator.
func TestWhateverFillsTheHoneypotIsAnsweredAsAStoredRequestAndNeverStored(t *testing.T) {
	w := bootForLeads(t)
	w.recipientsForLeads("le-tan-" + nonce(t) + "@example.com")
	stored := w.sendForm(leadForm("0966 000 000", nil), "")
	if stored.status != http.StatusAccepted || stored.body != `{"received":true}` || w.leadsOf("0966000000") != 1 {
		t.Fatalf("a request with no website answered %d %s and stored %d", stored.status, stored.body, w.leadsOf("0966000000"))
	}
	queued := func() int {
		t.Helper()
		var n int
		if err := w.pool.QueryRow(context.Background(), `SELECT count(*) FROM app.email_outbox WHERE template = 'lead_received'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	told := queued()
	if told != 1 {
		t.Fatalf("%d notices queued for the stored request, want 1", told)
	}

	for i, c := range []struct{ name, website string }{
		{"one character", "x"},
		{"one space", " "},
		{"a thousand characters", strings.Repeat("x", 1000)},
		{"as much as the body limit leaves room for", strings.Repeat("x", 3700)},
		{"an address", "https://spam.example/buy?now=1"},
		{"a phone number", "0903123456"},
		{"markup", `<script>alert("x")</script>`},
		{"the word null", "null"},
		{"the word false", "false"},
		{"lines and tabs", "a\n\tb\r\n"},
		{"text outside ASCII", "Mua ngay 🎁 giá rẻ"},
	} {
		digits := fmt.Sprintf("09770000%02d", i)
		got := w.sendForm(leadForm(digits, func(f map[string]any) { f["website"] = c.website }), "")
		if got.status != stored.status || got.body != stored.body || got.header.Get("Content-Type") != stored.header.Get("Content-Type") ||
			got.header.Get("Content-Length") != stored.header.Get("Content-Length") {
			t.Errorf("website of %s: answered %d %s (%s, %s bytes); a stored request answers %d %s (%s, %s bytes)", c.name,
				got.status, got.body, got.header.Get("Content-Type"), got.header.Get("Content-Length"),
				stored.status, stored.body, stored.header.Get("Content-Type"), stored.header.Get("Content-Length"))
		}
		if n := w.leadsOf(digits); n != 0 {
			t.Errorf("website of %s: %d requests stored, want none", c.name, n)
		}
	}
	if after := queued(); after != told {
		t.Errorf("%d notices were queued for requests that filled website", after-told)
	}

	untouched := w.sendForm(leadForm("0988 000 000", func(f map[string]any) { f["website"] = "" }), "")
	if untouched.status != stored.status || untouched.body != stored.body || w.leadsOf("0988000000") != 1 {
		t.Errorf("an empty website: answered %d %s and stored %d, want the request kept as one that left the field out",
			untouched.status, untouched.body, w.leadsOf("0988000000"))
	}

	nulSentence := regexp.MustCompile(`"(website|fullName)":"([^"]*)"`)
	inWebsite := w.sendForm(leadForm("0999 000 000", func(f map[string]any) { f["website"] = "a\x00b" }), "")
	inName := w.sendForm(leadForm("0999 000 000", func(f map[string]any) { f["fullName"] = "Ma\x00i" }), "")
	website, name := nulSentence.FindStringSubmatch(inWebsite.body), nulSentence.FindStringSubmatch(inName.body)
	if inWebsite.status != http.StatusBadRequest || inName.status != inWebsite.status || website == nil || name == nil ||
		website[1] != "website" || name[1] != "fullName" || website[2] != name[2] {
		t.Errorf("U+0000 in website answered %d %s and in fullName %d %s; the API refuses it in every string alike, and website gets no answer of its own",
			inWebsite.status, inWebsite.body, inName.status, inName.body)
	}
	if n := w.leadsOf("0999000000"); n != 0 {
		t.Errorf("%d requests stored from bodies holding U+0000", n)
	}
}

// TestOnlyAnAdminReachesTheConsultationRequestsAndNoRoleCanBeGrantedTheKey
// fails when a teacher or a student is let into any operation under
// /admin/leads, when the list is answered without a token, and when
// system.leads stops being a hidden key: a custom role that could be granted
// it would open a list of names and phone numbers to whoever holds the role.
// The hidden-key tests elsewhere name scope.all.
func TestOnlyAnAdminReachesTheConsultationRequestsAndNoRoleCanBeGrantedTheKey(t *testing.T) {
	w := bootForLeads(t)
	adminEmail, adminPassword := w.createStaff("admin")
	admin := w.signedIn(adminEmail, adminPassword)
	if got := w.sendForm(leadForm("0933 444 555", nil), ""); got.status != http.StatusAccepted {
		t.Fatalf("the form answered %d %s", got.status, got.body)
	}
	listed := admin.must(http.StatusOK, http.MethodGet, "/admin/leads", nil)
	leadID := id(listed["items"].([]any)[0].(map[string]any))

	operations := []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, "/admin/leads", nil},
		{http.MethodGet, "/admin/leads/" + leadID, nil},
		{http.MethodPatch, "/admin/leads/" + leadID, map[string]any{"status": "closed"}},
		{http.MethodDelete, "/admin/leads/" + leadID, nil},
	}
	teacherEmail, teacherPassword := w.createStaff("teacher")
	studentEmail, studentPassword := w.createStaff("student")
	for name, who := range map[string]*client{
		"a teacher": w.signedIn(teacherEmail, teacherPassword),
		"a student": w.signedIn(studentEmail, studentPassword),
	} {
		for _, op := range operations {
			if status, body := who.call(op.method, op.path, op.body); status != http.StatusForbidden || refusalCode(body) != "FORBIDDEN" {
				t.Errorf("%s on %s %s: %d %v, want 403 FORBIDDEN", name, op.method, op.path, status, body)
			}
		}
	}
	for _, op := range operations {
		if status, _ := w.browser().call(op.method, op.path, op.body); status != http.StatusUnauthorized {
			t.Errorf("nobody on %s %s: %d, want 401", op.method, op.path, status)
		}
	}
	if still := admin.must(http.StatusOK, http.MethodGet, "/admin/leads/"+leadID, nil); still["status"] != "new" {
		t.Errorf("a refused caller changed the request: %v", still)
	}

	created := admin.must(http.StatusCreated, http.MethodPost, "/admin/roles", map[string]any{"name": "Tư vấn viên " + nonce(t)})
	roleID := id(created)
	w.dropRoleAfter(roleID)
	status, body := admin.call(http.MethodPut, "/admin/roles/permissions", map[string]any{
		"changes":           []map[string]any{{"roleId": roleID, "key": "system.leads", "granted": true}},
		"expectedRevisions": []map[string]any{{"roleId": roleID, "revision": created["revision"]}},
	})
	if status == http.StatusOK {
		t.Fatalf("a custom role was granted system.leads: %v", body)
	}
	if status != http.StatusBadRequest && (status != http.StatusUnprocessableEntity || refusalCode(body) != "KEY_NOT_GRANTABLE") {
		t.Errorf("granting system.leads to a custom role: %d %v, want it refused as not grantable", status, body)
	}
	var held int
	if err := w.pool.QueryRow(context.Background(), `SELECT count(*) FROM app.role_permissions WHERE permission_key = 'system.leads'`).Scan(&held); err != nil || held != 0 {
		t.Errorf("%d roles hold system.leads (%v), want none", held, err)
	}
	matrix := admin.must(http.StatusOK, http.MethodGet, "/admin/roles", nil)
	if raw, _ := json.Marshal(matrix); strings.Contains(string(raw), "system.leads") {
		t.Errorf("the roles matrix offers system.leads")
	}
}
