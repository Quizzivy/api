//go:build integration

package repositories_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"quizzivy/internal/modules/leads/application"
	"quizzivy/internal/modules/leads/application/command"
	"quizzivy/internal/modules/leads/domain"
	"quizzivy/internal/modules/leads/repositories"
	"quizzivy/internal/platform/db"
	"quizzivy/internal/platform/email"
)

// TestAStoredRequestKeepsItsConsentAndTellsEachRecipientWithoutTheNumberOrTheName
// fails when the insert drops a field or the consent time, when a recipient
// gets no lead_received row or two, when the row is not written in the
// recipient account's language (or Vietnamese for an address no account has),
// and when any column of an outbox row holds the digits, the number as typed
// or the full name. No other test reads the outbox after a submission.
func TestAStoredRequestKeepsItsConsentAndTellsEachRecipientWithoutTheNumberOrTheName(t *testing.T) {
	w := newWorld(t)
	phone := randomPhone(t)
	typed := phone[:4] + " " + phone[4:7] + "-" + phone[7:]
	account := w.user("tu-van", "Thầy Tư Vấn", "en")
	w.recipients(w.address("le-tan"), w.address("tu-van"))

	w.submit(w.form(typed))

	ids := w.stored(phone)
	if len(ids) != 1 {
		t.Fatalf("%d requests stored for the number, want 1", len(ids))
	}
	got, err := w.repo.Get(w.ctx, ids[0])
	if err != nil {
		t.Fatal(err)
	}
	want := domain.Lead{
		ID: ids[0], FullName: typedName, PhoneDigits: phone, Interests: []string{"ielts", "kids"}, CourseFor: "child",
		CallTime: "evening", Locale: "vi", PrivacyNoticeVersion: "2026-11", ConsentedAt: w.now, Status: domain.StatusNew, CreatedAt: w.now,
	}
	if !got.ConsentedAt.Equal(want.ConsentedAt) || !got.CreatedAt.Equal(want.CreatedAt) {
		t.Errorf("consented at %v and created at %v, want both %v", got.ConsentedAt, got.CreatedAt, w.now)
	}
	got.ConsentedAt, got.CreatedAt = want.ConsentedAt, want.CreatedAt
	if got.ID != want.ID || got.FullName != want.FullName || got.PhoneDigits != want.PhoneDigits || !slices.Equal(got.Interests, want.Interests) ||
		got.CourseFor != want.CourseFor || got.CallTime != want.CallTime || got.Locale != want.Locale ||
		got.PrivacyNoticeVersion != want.PrivacyNoticeVersion || got.Status != want.Status || got.Note != "" || got.HandledBy != nil || got.HandledAt != nil {
		t.Errorf("stored request = %+v, want %+v", got, want)
	}

	rows := w.outbox(ids[0])
	if len(rows) != 2 {
		t.Fatalf("%d outbox rows, want one for each of the two recipients", len(rows))
	}
	bare, linked := rows[0], rows[1]
	if bare.to != w.address("le-tan") || bare.userID != nil || bare.locale != "vi" {
		t.Errorf("the address no account has: to %q, user %v, locale %q; want it bare and in Vietnamese", bare.to, bare.userID, bare.locale)
	}
	if linked.to != w.address("tu-van") || linked.userID == nil || *linked.userID != account || linked.locale != "en" {
		t.Errorf("the address of an account: to %q, user %v, locale %q; want the account and its English", linked.to, linked.userID, linked.locale)
	}
	for _, row := range rows {
		if row.template != "lead_received" {
			t.Errorf("template = %q", row.template)
		}
		var params email.LeadReceivedParams
		if err := json.Unmarshal([]byte(row.params), &params); err != nil {
			t.Fatalf("params %s: %v", row.params, err)
		}
		if params.LeadID != ids[0] || params.GivenName != "Mai" || !slices.Equal(params.Interests, []string{"ielts", "kids"}) ||
			params.CourseFor != "child" || params.CallTime != "evening" || !params.ReceivedAt.Equal(w.now) {
			t.Errorf("params = %+v", params)
		}
		for what, held := range map[string]string{"the digits": phone, "the number as typed": typed, "the full name": typedName, "the family name": "Trần", "the middle name": "Thị"} {
			if strings.Contains(row.whole, held) {
				t.Errorf("the outbox row to %s holds %s: %s", row.to, what, row.whole)
			}
		}
	}
}

// TestWithoutRecipientsARequestIsStoredAndNobodyIsTold fails when an empty
// recipient list stops the request being stored, or a message is queued for
// nobody.
func TestWithoutRecipientsARequestIsStoredAndNobodyIsTold(t *testing.T) {
	w := newWorld(t)
	phone := randomPhone(t)
	before := w.count(`SELECT count(*) FROM app.email_outbox`)
	w.submit(w.form(phone))
	ids := w.stored(phone)
	if len(ids) != 1 {
		t.Fatalf("%d requests stored, want 1", len(ids))
	}
	if after := w.count(`SELECT count(*) FROM app.email_outbox`); after != before {
		t.Errorf("%d messages were queued with no recipient", after-before)
	}
}

// TestTheThirdRequestOfANumberInADayIsAnsweredAndNotStored fails when the
// count that caps a number ignores the 24-hour window (an old request then
// blocks a new one for ever, or a recent one stops counting), counts another
// number's requests, or lets a honeypot through; and when a dropped request
// still tells the recipients. The domain's verdict test covers the cap as a
// number; this one covers the query that feeds it.
func TestTheThirdRequestOfANumberInADayIsAnsweredAndNotStored(t *testing.T) {
	w := newWorld(t)
	w.recipients(w.address("le-tan"))
	phone, other := randomPhone(t), randomPhone(t)
	start := w.now

	w.submit(w.form(phone))
	w.now = start.Add(time.Hour)
	w.submit(w.form(phone))
	w.now = start.Add(2 * time.Hour)
	w.submit(w.form(phone))
	if got := w.stored(phone); len(got) != 2 {
		t.Fatalf("%d requests stored after three in two hours, want 2", len(got))
	}
	w.submit(w.form(other))
	if got := w.stored(other); len(got) != 1 {
		t.Errorf("another number's first request: %d stored, want 1", len(got))
	}

	w.now = start.Add(23*time.Hour + 59*time.Minute)
	w.submit(w.form(phone))
	if got := w.stored(phone); len(got) != 2 {
		t.Fatalf("%d stored a minute before the first request is a day old, want 2", len(got))
	}
	w.now = start.Add(24 * time.Hour)
	w.submit(w.form(phone))
	ids := w.stored(phone)
	if len(ids) != 3 {
		t.Fatalf("%d stored once the first request is a day old, want 3", len(ids))
	}
	w.now = start.Add(24*time.Hour + time.Minute)
	w.submit(w.form(phone))
	if got := w.stored(phone); len(got) != 3 {
		t.Errorf("%d stored with two requests again inside the day, want 3", len(got))
	}

	trapped := w.form(randomPhone(t))
	trapped.Website = "https://spam.example"
	w.submit(trapped)
	if got := w.stored(domain.Digits(trapped.Phone)); len(got) != 0 {
		t.Errorf("a honeypot's request was stored")
	}

	told := w.count(`SELECT count(*) FROM app.email_outbox WHERE to_address = $1`, w.address("le-tan"))
	if told != 4 {
		t.Errorf("%d messages queued, want 4: one for each stored request and none for a dropped one", told)
	}
}

// TestSubmissionsAtOnceForOneNumberAreJudgedOneAfterTheOther fails when Submit
// stops taking its lock on the number: eight submissions that all count before
// any has inserted would each see none stored and all be kept. It commits, so
// it removes its rows itself; every other test here rolls back.
func TestSubmissionsAtOnceForOneNumberAreJudgedOneAfterTheOther(t *testing.T) {
	pool := connect(t)
	ctx := context.Background()
	phone := randomPhone(t)
	t.Cleanup(func() {
		if _, err := pool.Exec(ctx, `DELETE FROM app.email_outbox o USING app.leads l WHERE o.params->>'leadId' = l.id::text AND l.phone_digits = $1`, phone); err != nil {
			t.Errorf("removing the test's messages: %v", err)
		}
		if _, err := pool.Exec(ctx, `DELETE FROM app.leads WHERE phone_digits = $1`, phone); err != nil {
			t.Errorf("removing the test's requests: %v", err)
		}
	})
	app := application.New(repositories.NewPostgres(db.NewContext(pool), email.NewOutbox(nil)), slog.New(slog.DiscardHandler))
	form := domain.Form{
		FullName: typedName, Phone: phone, Interests: []string{"toeic"}, CourseFor: "self",
		CallTime: "any", Locale: "en", PrivacyNoticeVersion: "2026-11",
	}
	const senders = 8
	start := make(chan struct{})
	errs := make(chan error, senders)
	var wg sync.WaitGroup
	for range senders {
		wg.Go(func() {
			<-start
			_, err := app.Commands.Submit.Handle(ctx, command.Submit{Form: form})
			errs <- err
		})
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("submit: %v", err)
		}
	}
	var stored int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM app.leads WHERE phone_digits = $1`, phone).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != domain.RepeatCap {
		t.Errorf("%d of %d submissions at once were stored, want %d", stored, senders, domain.RepeatCap)
	}
}

func diffOf(t *testing.T, row auditRow) map[string]any {
	t.Helper()
	if row.diff == nil {
		t.Fatalf("the %s entry has no diff", row.action)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(*row.diff), &out); err != nil {
		t.Fatalf("diff %s: %v", *row.diff, err)
	}
	return out
}

// TestAnUpdateRecordsWhoHandledARequestOnlyWhenItsStatusMoves fails when a
// note alone names a handler, when a status that differs does not, when the
// handler's name is not served, and when the audit entry of a change is
// missing, names the wrong actor, or holds more than the status before and
// after and whether the note changed.
func TestAnUpdateRecordsWhoHandledARequestOnlyWhenItsStatusMoves(t *testing.T) {
	w := newWorld(t)
	id := w.lead(typedName, randomPhone(t), "new", w.now.Add(-time.Hour))

	noted := w.update(id, domain.Change{NoteSet: true, Note: leadNote})
	if noted.Note != leadNote || noted.Status != domain.StatusNew || noted.HandledBy != nil || noted.HandledAt != nil {
		t.Fatalf("after a note alone = %+v, want the note and nobody named as handler", noted)
	}

	w.now = w.now.Add(10 * time.Minute)
	moved := w.update(id, domain.Change{Status: status(domain.StatusContacted)})
	if moved.Status != domain.StatusContacted || moved.Note != leadNote || moved.HandledAt == nil || !moved.HandledAt.Equal(w.now) ||
		moved.HandledBy == nil || moved.HandledBy.ID != w.actor || moved.HandledBy.Name != "Cô Thương" {
		t.Fatalf("after a status change = %+v (handler %+v), want the actor and the time, and the note kept", moved, moved.HandledBy)
	}
	firstHandled := *moved.HandledAt

	other := w.user("le-tan", "Chị Lễ Tân", "vi")
	w.actor, w.now = other, w.now.Add(10*time.Minute)
	both := w.update(id, domain.Change{Status: status(domain.StatusClosed), NoteSet: true, Note: ""})
	if both.Status != domain.StatusClosed || both.Note != "" || both.HandledBy == nil || both.HandledBy.ID != other || !both.HandledAt.After(firstHandled) {
		t.Fatalf("after a status and a removed note = %+v (handler %+v)", both, both.HandledBy)
	}
	if reread, err := w.repo.Get(w.ctx, id); err != nil || reread.Status != both.Status || reread.Note != "" || reread.HandledBy == nil || reread.HandledBy.ID != other {
		t.Errorf("read after the update = %+v, %v", reread, err)
	}

	entries := w.audits(id)
	if len(entries) != 3 {
		t.Fatalf("%d audit entries, want one for each change", len(entries))
	}
	wantDiffs := []map[string]any{
		{"noteChanged": true},
		{"status": map[string]any{"old": "new", "new": "contacted"}},
		{"status": map[string]any{"old": "contacted", "new": "closed"}, "noteChanged": true},
	}
	for i, entry := range entries {
		if entry.action != "lead.updated" || entry.entity != "lead" || entry.ip == nil || *entry.ip != "203.0.113.9" ||
			entry.agent == nil || *entry.agent != "leads-test/"+w.nonce {
			t.Errorf("entry %d = %+v", i, entry)
		}
		got, _ := json.Marshal(diffOf(t, entry))
		want, _ := json.Marshal(wantDiffs[i])
		if string(got) != string(want) {
			t.Errorf("entry %d diff = %s, want %s", i, got, want)
		}
	}
	if entries[2].actor == nil || *entries[2].actor != other || entries[1].actor == nil || *entries[1].actor == other {
		t.Errorf("the entries name %v then %v as actors, want each change's own", entries[1].actor, entries[2].actor)
	}
	if !entries[2].occurred.Equal(w.now) {
		t.Errorf("the last entry occurred at %v, want %v", entries[2].occurred, w.now)
	}
}

// TestAnUpdateThatChangesNothingWritesNothing fails when a body that restates
// the stored status and note touches the row (updated_at moves), names a
// handler, or leaves an audit entry; and when a missing request is answered as
// anything but ErrNotFound.
func TestAnUpdateThatChangesNothingWritesNothing(t *testing.T) {
	w := newWorld(t)
	id := w.lead(typedName, randomPhone(t), "new", w.now.Add(-time.Hour))
	w.update(id, domain.Change{Status: status(domain.StatusContacted), NoteSet: true, Note: leadNote})
	var before time.Time
	if err := w.tx.QueryRow(w.ctx, `UPDATE app.leads SET updated_at = $2 WHERE id = $1::uuid RETURNING updated_at`, id, w.now.Add(-time.Minute)).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := w.tx.QueryRow(w.ctx, `SELECT updated_at FROM app.leads WHERE id = $1::uuid`, id).Scan(&before); err != nil {
		t.Fatal(err)
	}
	handler := w.actor
	w.actor = w.user("nguoi-khac", "Người Khác", "vi")
	for name, change := range map[string]domain.Change{
		"the same status":          {Status: status(domain.StatusContacted)},
		"the same note":            {NoteSet: true, Note: leadNote},
		"the same status and note": {Status: status(domain.StatusContacted), NoteSet: true, Note: leadNote},
		"nothing at all":           {},
	} {
		got := w.update(id, change)
		if got.Status != domain.StatusContacted || got.Note != leadNote || got.HandledBy == nil || got.HandledBy.ID != handler {
			t.Errorf("%s: returned %+v (handler %+v), want the request as stored", name, got, got.HandledBy)
		}
	}
	var after time.Time
	if err := w.tx.QueryRow(w.ctx, `SELECT updated_at FROM app.leads WHERE id = $1::uuid`, id).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if !after.Equal(before) {
		t.Errorf("updated_at moved from %v to %v", before, after)
	}
	if entries := w.audits(id); len(entries) != 1 {
		t.Errorf("%d audit entries, want only the one of the real change", len(entries))
	}

	missing := "019535d9-3df7-79fb-b466-fa907fa17f02"
	_, err := w.repo.Update(w.ctx, domain.Update{ID: missing, Change: domain.Change{Status: status(domain.StatusClosed)}, ActorID: w.actor, Now: w.now})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("updating a missing request: %v, want ErrNotFound", err)
	}
	if _, err := w.repo.Get(w.ctx, missing); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("reading a missing request: %v, want ErrNotFound", err)
	}
	if n := w.count(`SELECT count(*) FROM app.audit_log WHERE entity_id = $1::uuid`, missing); n != 0 {
		t.Errorf("%d audit entries about a request that does not exist", n)
	}
}

// TestADeleteRemovesTheRequestAndNoAuditEntryEverHeldWhatItSaid fails when the
// delete leaves the row, writes no lead.deleted entry or one with a diff, and
// when any audit entry about the request, from its changes or its removal,
// holds the note, the name, a word of it or the number: the log outlives the
// request's 12 months.
func TestADeleteRemovesTheRequestAndNoAuditEntryEverHeldWhatItSaid(t *testing.T) {
	w := newWorld(t)
	phone := randomPhone(t)
	id := w.lead(typedName, phone, "new", w.now.Add(-time.Hour))
	w.update(id, domain.Change{NoteSet: true, Note: leadNote})
	w.update(id, domain.Change{Status: status(domain.StatusContacted), NoteSet: true, Note: leadNote + " (đã gọi " + phone + ")"})

	del := domain.Delete{ID: id, ActorID: w.actor, IP: "203.0.113.9", UserAgent: "leads-test/" + w.nonce, Now: w.now.Add(time.Minute)}
	if err := w.repo.Delete(w.ctx, del); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if n := w.count(`SELECT count(*) FROM app.leads WHERE id = $1::uuid`, id); n != 0 {
		t.Fatal("the request is still stored")
	}
	if err := w.repo.Delete(w.ctx, del); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("deleting it again: %v, want ErrNotFound", err)
	}

	entries := w.audits(id)
	if len(entries) != 3 {
		t.Fatalf("%d audit entries, want two changes and one removal", len(entries))
	}
	last := entries[2]
	if last.action != "lead.deleted" || last.entity != "lead" || last.diff != nil || last.actor == nil || *last.actor != w.actor ||
		last.ip == nil || *last.ip != "203.0.113.9" || !last.occurred.Equal(del.Now) {
		t.Errorf("the removal's entry = %+v, want lead.deleted naming the actor and holding no diff", last)
	}
	for _, entry := range entries {
		for what, held := range map[string]string{"the note": leadNote, "a word of the note": "IELTS", "the name": typedName, "the given name": "Mai", "the number": phone} {
			if strings.Contains(entry.whole, held) {
				t.Errorf("the %s entry holds %s: %s", entry.action, what, entry.whole)
			}
		}
	}
}

// TestTheListFindsARequestByNameWithoutAccentsOrByTheDigitsOfItsNumber fails
// when the search stops folding accents and case, compares the number with
// the punctuation of the search still in it, treats % or _ as a wildcard,
// ignores the status filter, or orders anything but newest first; and when
// the page's total is not the number of matches.
func TestTheListFindsARequestByNameWithoutAccentsOrByTheDigitsOfItsNumber(t *testing.T) {
	w := newWorld(t)
	tag := "zq" + w.nonce
	anhPhone, binhPhone := randomPhone(t), randomPhone(t)
	anh := w.lead("Nguyễn Thị Ánh "+tag, anhPhone, "new", w.now.Add(-3*time.Hour))
	binh := w.lead("Trần Văn Bình "+tag, binhPhone, "contacted", w.now.Add(-2*time.Hour))
	leAnh := w.lead("Lê ANH "+tag, randomPhone(t), "new", w.now.Add(-time.Hour))

	list := func(q domain.ListQuery) ([]string, int) {
		t.Helper()
		leads, page, err := w.repo.List(w.ctx, q)
		if err != nil {
			t.Fatalf("list %+v: %v", q, err)
		}
		ids := make([]string, len(leads))
		for i, l := range leads {
			ids[i] = l.ID
		}
		return ids, page.Total
	}
	for name, c := range map[string]struct {
		q    domain.ListQuery
		want []string
	}{
		"the tag, newest first":            {domain.ListQuery{Q: tag}, []string{leAnh, binh, anh}},
		"a name without its accent":        {domain.ListQuery{Q: "anh " + tag}, []string{leAnh, anh}},
		"a name in other case and accents": {domain.ListQuery{Q: "trần văn BÌNH " + tag}, []string{binh}},
		"one status":                       {domain.ListQuery{Q: tag, Status: domain.StatusContacted}, []string{binh}},
		"a wildcard is a character":        {domain.ListQuery{Q: "%" + tag}, nil},
		"an underscore is a character":     {domain.ListQuery{Q: "Lê_ANH " + tag}, nil},
		"the number as someone types it":   {domain.ListQuery{Q: "(" + anhPhone[:4] + ") " + anhPhone[4:7] + "-" + anhPhone[7:]}, []string{anh}},
		"the number's digits":              {domain.ListQuery{Q: binhPhone}, []string{binh}},
		"the number, in another status":    {domain.ListQuery{Q: binhPhone, Status: domain.StatusNew}, nil},
	} {
		got, total := list(c.q)
		if !slices.Equal(got, c.want) || total != len(c.want) {
			t.Errorf("%s: %v (total %d), want %v", name, got, total, c.want)
		}
	}

	first, total := list(domain.ListQuery{Q: tag, Page: 1, Limit: 2})
	second, _ := list(domain.ListQuery{Q: tag, Page: 2, Limit: 2})
	if total != 3 || !slices.Equal(first, []string{leAnh, binh}) || !slices.Equal(second, []string{anh}) {
		t.Errorf("pages of two: %v then %v of %d, want the two newest then the oldest of 3", first, second, total)
	}
}
