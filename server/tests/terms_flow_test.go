//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"math/rand/v2"
	"net/http"
	"testing"
)

// termsRig is an Admin and a Teacher, and a year of their own: terms are
// organisation-wide and never overlap, so each run writes far from any other
// test's days, and deletes what it wrote.
type termsRig struct {
	t       *testing.T
	w       *world
	admin   *client
	teacher *client
	year    int
	written []string
}

func newTermsRig(t *testing.T) *termsRig {
	t.Helper()
	w := boot(t)
	r := &termsRig{t: t, w: w, year: 3000 + rand.IntN(6000)}
	r.admin = w.signedIn(w.createStaff("admin"))
	r.teacher = w.signedIn(w.createStaff("teacher"))
	t.Cleanup(func() {
		if _, err := w.pool.Exec(context.Background(), `DELETE FROM app.terms WHERE id = ANY($1::uuid[])`, r.written); err != nil {
			t.Errorf("deleting the run's terms: %v", err)
		}
	})
	return r
}

func (r *termsRig) day(years, month, day int) string {
	return fmt.Sprintf("%04d-%02d-%02d", r.year+years, month, day)
}

func (r *termsRig) create(name, from, to string) map[string]any {
	r.t.Helper()
	body := r.admin.must(http.StatusCreated, http.MethodPost, "/admin/settings/terms", map[string]any{"name": name, "startsOn": from, "endsOn": to})
	r.written = append(r.written, id(body))
	return body
}

func termError(body map[string]any) (code string, details map[string]any) {
	e, _ := body["error"].(map[string]any)
	code, _ = e["code"].(string)
	details, _ = e["details"].(map[string]any)
	return code, details
}

// An Admin adds, lists, edits and removes terms over HTTP; an overlap answers
// 409 TERM_OVERLAPS naming the other term, decided by the database.
func TestAnAdminKeepsTheCentresTerms(t *testing.T) {
	r := newTermsRig(t)
	autumn := r.create("  Học kỳ 1  ", r.day(0, 9, 1), r.day(0, 12, 31))
	if autumn["name"] != "Học kỳ 1" || autumn["startsOn"] != r.day(0, 9, 1) || autumn["endsOn"] != r.day(0, 12, 31) || autumn["state"] != "upcoming" {
		t.Errorf("created %v, want the trimmed name, the range sent and upcoming", autumn)
	}
	spring := r.create("Học kỳ 2", r.day(1, 1, 1), r.day(1, 5, 31))

	listed := r.admin.must(http.StatusOK, http.MethodGet, "/admin/settings/terms", nil)["items"].([]any)
	var order []string
	for _, item := range listed {
		if term := item.(map[string]any); term["id"] == id(autumn) || term["id"] == id(spring) {
			order = append(order, term["id"].(string))
		}
	}
	if len(order) != 2 || order[0] != id(spring) || order[1] != id(autumn) {
		t.Errorf("listed %v, want spring then autumn", order)
	}

	status, body := r.admin.call(http.MethodPost, "/admin/settings/terms", map[string]any{"name": "Hè", "startsOn": r.day(0, 12, 31), "endsOn": r.day(1, 2, 1)})
	if code, details := termError(body); status != http.StatusConflict || code != "TERM_OVERLAPS" || details["termId"] != id(autumn) || details["name"] != "Học kỳ 1" {
		t.Errorf("a term starting on autumn's last day: %d %v, want 409 TERM_OVERLAPS naming autumn", status, body)
	}
	status, body = r.admin.call(http.MethodPatch, "/admin/settings/terms/"+id(spring), map[string]any{"startsOn": r.day(0, 12, 15)})
	if code, details := termError(body); status != http.StatusConflict || code != "TERM_OVERLAPS" || details["termId"] != id(autumn) {
		t.Errorf("moving spring into autumn: %d %v, want 409 TERM_OVERLAPS naming autumn", status, body)
	}
	status, body = r.admin.call(http.MethodPatch, "/admin/settings/terms/"+id(spring), map[string]any{"endsOn": r.day(0, 12, 1)})
	if code, details := termError(body); status != http.StatusBadRequest || code != "VALIDATION_FAILED" || details["endsOn"] == nil {
		t.Errorf("ending spring before it starts: %d %v, want 400 naming endsOn", status, body)
	}
	status, body = r.admin.call(http.MethodPost, "/admin/settings/terms", map[string]any{"name": "   ", "startsOn": r.day(2, 1, 1), "endsOn": r.day(2, 1, 2)})
	if code, details := termError(body); status != http.StatusBadRequest || code != "VALIDATION_FAILED" || details["name"] == nil {
		t.Errorf("a blank name: %d %v, want 400 naming name", status, body)
	}

	saved := r.admin.must(http.StatusOK, http.MethodPatch, "/admin/settings/terms/"+id(spring), map[string]any{"name": "Học kỳ xuân", "endsOn": r.day(1, 6, 15)})
	if saved["name"] != "Học kỳ xuân" || saved["startsOn"] != r.day(1, 1, 1) || saved["endsOn"] != r.day(1, 6, 15) {
		t.Errorf("saved %v, want the new name and end, the start kept", saved)
	}

	r.admin.must(http.StatusNoContent, http.MethodDelete, "/admin/settings/terms/"+id(spring), nil)
	r.admin.must(http.StatusNotFound, http.MethodDelete, "/admin/settings/terms/"+id(spring), nil)
	r.admin.must(http.StatusNotFound, http.MethodPatch, "/admin/settings/terms/"+id(spring), map[string]any{"name": "Không còn"})

	var audited int
	if err := r.w.pool.QueryRow(context.Background(), `SELECT count(*) FROM app.audit_log
		WHERE entity = 'term' AND entity_id = $1::uuid AND action IN ('term.created', 'term.updated', 'term.deleted')`, id(spring)).Scan(&audited); err != nil || audited != 3 {
		t.Errorf("%d audit rows for spring (%v), want created, updated and deleted", audited, err)
	}
}

// Terms are organisation-wide and have no owner, and /admin/* is outside the
// isolation suite's scoped trees, so this is their scoping check: the
// permission gate refuses a Teacher every terms operation, and the term the
// Teacher aimed at is untouched.
func TestATeacherIsRefusedEveryTermsOperation(t *testing.T) {
	r := newTermsRig(t)
	term := r.create("Học kỳ 1", r.day(0, 9, 1), r.day(0, 12, 31))
	path := "/admin/settings/terms/" + id(term)
	cases := []struct {
		method, path string
		body         map[string]any
	}{
		{http.MethodGet, "/admin/settings/terms", nil},
		{http.MethodPost, "/admin/settings/terms", map[string]any{"name": "Của giáo viên", "startsOn": r.day(1, 1, 1), "endsOn": r.day(1, 2, 1)}},
		{http.MethodPatch, path, map[string]any{"name": "Đổi tên"}},
		{http.MethodDelete, path, nil},
	}
	for _, c := range cases {
		status, body := r.teacher.call(c.method, c.path, c.body)
		if code, _ := termError(body); status != http.StatusForbidden || code != "FORBIDDEN" {
			t.Errorf("a Teacher's %s %s: %d %v, want 403 FORBIDDEN", c.method, c.path, status, body)
		}
	}
	var name string
	var count int
	if err := r.w.pool.QueryRow(context.Background(), `SELECT name, (SELECT count(*) FROM app.terms WHERE name = 'Của giáo viên') FROM app.terms WHERE id = $1::uuid`, id(term)).Scan(&name, &count); err != nil || name != "Học kỳ 1" || count != 0 {
		t.Errorf("after the Teacher's calls the term is %q (%v) and %d of theirs exist, want it untouched and none", name, err, count)
	}
}
