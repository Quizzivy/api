//go:build integration

package maintenance_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"quizzivy/internal/core/maintenance"
)

const (
	leadName  = "Trần Thị Mai"
	leadPhone = "0903123456"
	leadNote  = "Gọi lại sau giờ tan học"
)

func (w *sweepWorld) lead(created time.Time, status string) string {
	w.t.Helper()
	return w.id(`
		INSERT INTO app.leads
		       (full_name, phone_digits, interests, course_for, call_time, locale, privacy_notice_version,
		        consented_at, created_at, status, note)
		VALUES ($1, $2, '{ielts}', 'self', 'any', 'vi', '2026-11', $3, $3, $4, $5)
		RETURNING id::text`, leadName, leadPhone, created, status, leadNote)
}

func (w *sweepWorld) leadExists(id string) bool {
	w.t.Helper()
	return w.count(`SELECT count(*) FROM app.leads WHERE id = $1`, id) == 1
}

// TestTheLeadsStepDeletesARequestTwelveMonthsAfterItWasSentAndKeepsAYoungerOne
// fails when the sweep loses its leads step, when the step's cutoff is not 12
// months, when it keeps a request because it was handled, when a dry run
// deletes, and, in the maintenance role's world, when the role lacks SELECT
// (id, created_at) or DELETE on app.leads or INSERT on app.audit_log (42501).
// It also fails when the audit entry carries what the request held. No other
// test runs the sweep over a consultation request.
func TestTheLeadsStepDeletesARequestTwelveMonthsAfterItWasSentAndKeepsAYoungerOne(t *testing.T) {
	eachWorld(t, func(t *testing.T, w *sweepWorld) {
		w.retention(nil, 13, nil)
		cutoff := w.cutoff(12)
		old := w.lead(cutoff.Add(-w.margin), "new")
		handled := w.lead(cutoff.Add(-48*time.Hour), "closed")
		young := w.lead(cutoff.Add(w.margin), "new")
		today := w.lead(cutoff.AddDate(0, 12, 0).Add(-time.Hour), "contacted")

		dry := w.sweep(maintenance.SweepOptions{})
		if dry.Leads.Leads != 2 || !w.sameInstant(dry.Leads.Cutoff, cutoff) {
			t.Errorf("the dry run's leads step = %+v, want 2 requests before %s", dry.Leads, cutoff)
		}
		if !w.leadExists(old) || !w.leadExists(handled) || w.audits("retention.leads_removed") != 0 {
			t.Fatal("a dry run deleted a request or wrote an audit entry")
		}

		report := w.sweep(maintenance.SweepOptions{Apply: true})
		if report.Leads.Leads != 2 || report.Leads.Batches != 1 || !w.sameInstant(report.Leads.Cutoff, cutoff) || !report.Complete {
			t.Errorf("leads step = %+v (complete %v), want 2 requests in one batch before %s", report.Leads, report.Complete, cutoff)
		}
		if w.leadExists(old) || w.leadExists(handled) {
			t.Error("a request older than 12 months was kept")
		}
		if !w.leadExists(young) || !w.leadExists(today) {
			t.Error("a request younger than 12 months was deleted")
		}

		var actors, entries int
		var entity string
		var diff []byte
		if err := w.q.QueryRow(w.ctx, `
			SELECT count(*), count(actor_user_id), min(entity), min(diff::text)
			  FROM app.audit_log WHERE action = 'retention.leads_removed'`).Scan(&entries, &actors, &entity, &diff); err != nil {
			t.Fatal(err)
		}
		if entries != 1 || actors != 0 || entity != "leads" {
			t.Fatalf("%d audit entries about %q, %d with an actor; want one about leads with none", entries, entity, actors)
		}
		var logged struct {
			Cutoff       time.Time `json:"cutoff"`
			Leads        int       `json:"leads"`
			DatabaseRole string    `json:"databaseRole"`
		}
		if err := json.Unmarshal(diff, &logged); err != nil {
			t.Fatalf("audit diff %s: %v", diff, err)
		}
		if logged.Leads != 2 || !w.sameInstant(logged.Cutoff, cutoff) || logged.DatabaseRole == "" {
			t.Errorf("audit diff = %s, want 2 requests, the cutoff and the role", diff)
		}
		for _, held := range []string{leadName, leadPhone, leadNote, old, handled} {
			if strings.Contains(string(diff), held) {
				t.Errorf("the audit entry holds %q of a deleted request: %s", held, diff)
			}
		}

		again := w.sweep(maintenance.SweepOptions{Apply: true})
		if again.Leads.Leads != 0 || w.audits("retention.leads_removed") != 1 {
			t.Errorf("a second run = %+v with %d audit entries, want nothing to do and no further entry", again.Leads, w.audits("retention.leads_removed"))
		}
	})
}

// TestTheLeadsStepWorksInBatchesOldestFirstWithAnAuditEntryForEach fails when
// the step ignores -batch, stops after its first batch, or writes one audit
// entry for the whole run instead of one a batch, which is what lets a run the
// budget ends account for what it did. The answers step's batch test covers
// attempts, not requests.
func TestTheLeadsStepWorksInBatchesOldestFirstWithAnAuditEntryForEach(t *testing.T) {
	w := newSweepWorld(t)
	w.retention(nil, 13, nil)
	cutoff := w.cutoff(12)
	for i := range 5 {
		w.lead(cutoff.Add(-time.Duration(i+1)*time.Hour), "new")
	}
	kept := w.lead(cutoff.Add(time.Hour), "new")

	report := w.sweep(maintenance.SweepOptions{Apply: true, Batch: 2})
	if report.Leads.Leads != 5 || report.Leads.Batches != 3 {
		t.Errorf("leads step = %+v, want 5 requests in 3 batches", report.Leads)
	}
	if got := w.count(`SELECT count(*) FROM app.leads`); got != 1 || !w.leadExists(kept) {
		t.Errorf("%d requests remain, want the one younger than the cutoff", got)
	}
	if got := w.audits("retention.leads_removed"); got != 3 {
		t.Errorf("%d audit entries for 3 batches", got)
	}
	if total := w.count(`SELECT coalesce(sum((diff->>'leads')::int), 0) FROM app.audit_log WHERE action = 'retention.leads_removed'`); total != 5 {
		t.Errorf("the audit entries count %d requests, want 5", total)
	}
}
