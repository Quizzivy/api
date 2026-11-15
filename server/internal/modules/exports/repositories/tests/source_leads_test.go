//go:build integration

package repositories_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"quizzivy/internal/core/adapters"
	"quizzivy/internal/modules/exports/domain"
)

// TestLeadsAreExportedWithTheirConsentAndWrittenSoNoCellRunsAsAFormula fails
// when leads.csv leaves the export, drops the consent a request carried or
// how the centre handled it, and when a name or a note a visitor typed to
// begin with = or + reaches the CSV unescaped. The users test covers the
// same writer for users.csv; a consultation request is the one file whose
// every text cell comes from someone outside the centre.
func TestLeadsAreExportedWithTheirConsentAndWrittenSoNoCellRunsAsAFormula(t *testing.T) {
	w := newWorld(t)
	handler := w.user("admin", "Cô Thương")
	const name, note = `=HYPERLINK("http://x")`, "+84 gọi lại sau 18h"
	var id string
	err := w.pool.QueryRow(context.Background(), `
		INSERT INTO app.leads
		       (full_name, phone_digits, interests, course_for, call_time, locale, privacy_notice_version,
		        consented_at, created_at, status, note, handled_by, handled_at)
		VALUES ($1, '0903123456', '{ielts,business}', 'company', 'afternoon', 'en', '2026-11',
		        '2026-11-01 02:03:04+00', '2026-11-01 02:03:04+00', 'contacted', $2, $3::uuid, '2026-11-02 05:06:07+00')
		RETURNING id::text`, name, note, handler).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := w.pool.Exec(context.Background(), `DELETE FROM app.leads WHERE id = $1::uuid`, id); err != nil {
			t.Errorf("removing the request: %v", err)
		}
	})

	got := w.read(false, nil)
	row := got.rowWith(domain.LeadsFile, column(t, domain.LeadsFile, "id"), id)
	if row == nil {
		t.Fatal("the request is not in leads.csv")
	}
	for header, want := range map[string]string{
		"full_name": name, "phone": "0903123456", "interests": "ielts;business", "course_for": "company",
		"call_time": "afternoon", "locale": "en", "privacy_notice_version": "2026-11",
		"consented_at": "2026-11-01T02:03:04Z", "status": "contacted", "note": note, "handled_by": handler,
		"handled_at": "2026-11-02T05:06:07Z", "created_at": "2026-11-01T02:03:04Z",
	} {
		if cell := row[column(t, domain.LeadsFile, header)]; cell != want {
			t.Errorf("%s = %q, want %q", header, cell, want)
		}
	}
	if updated := row[column(t, domain.LeadsFile, "updated_at")]; !isoUTC.MatchString(updated) {
		t.Errorf("updated_at = %q is not an ISO 8601 UTC time", updated)
	}

	var csv bytes.Buffer
	table, err := adapters.ExportTable(&csv)
	if err != nil {
		t.Fatal(err)
	}
	if err := table.Row(row...); err != nil {
		t.Fatal(err)
	}
	if err := table.Flush(); err != nil {
		t.Fatal(err)
	}
	written := csv.String()
	if !strings.Contains(written, `"'=HYPERLINK(""http://x"")"`) || !strings.Contains(written, "'+84 gọi lại sau 18h") {
		t.Errorf("the row as the export writes it lets a formula through: %s", written)
	}
	if strings.Contains(written, `,=HYPERLINK`) || strings.Contains(written, `,+84`) {
		t.Errorf("a cell begins with a formula character: %s", written)
	}
}
