//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

type ownedRows struct {
	Classes, Tests, Questions, Groups, Sets, Media, Assignments int
}

func (w *world) ownedBy(userID string) ownedRows {
	w.t.Helper()
	var o ownedRows
	err := w.pool.QueryRow(context.Background(), `
		SELECT (SELECT count(*) FROM app.classes WHERE teacher_id = $1::uuid)::int,
		       (SELECT count(*) FROM app.tests WHERE owner_id = $1::uuid)::int,
		       (SELECT count(*) FROM app.questions WHERE owner_id = $1::uuid)::int,
		       (SELECT count(*) FROM app.question_groups WHERE owner_id = $1::uuid)::int,
		       (SELECT count(*) FROM app.question_sets WHERE owner_id = $1::uuid)::int,
		       (SELECT count(*) FROM app.media_assets WHERE owner_id = $1::uuid)::int,
		       (SELECT count(*) FROM app.assignments WHERE created_by = $1::uuid)::int`, userID).
		Scan(&o.Classes, &o.Tests, &o.Questions, &o.Groups, &o.Sets, &o.Media, &o.Assignments)
	if err != nil {
		w.t.Fatal(err)
	}
	return o
}

func (w *world) liveOwnedBy(userID string) map[string]float64 {
	w.t.Helper()
	var o ownedRows
	err := w.pool.QueryRow(context.Background(), `
		SELECT (SELECT count(*) FROM app.classes WHERE teacher_id = $1::uuid)::int,
		       (SELECT count(*) FROM app.tests WHERE owner_id = $1::uuid AND deleted_at IS NULL)::int,
		       (SELECT count(*) FROM app.questions WHERE owner_id = $1::uuid AND deleted_at IS NULL)::int,
		       (SELECT count(*) FROM app.question_groups WHERE owner_id = $1::uuid AND archived_at IS NULL)::int,
		       (SELECT count(*) FROM app.question_sets WHERE owner_id = $1::uuid AND deleted_at IS NULL)::int,
		       (SELECT count(*) FROM app.media_assets WHERE owner_id = $1::uuid AND deleted_at IS NULL)::int,
		       (SELECT count(*) FROM app.assignments WHERE created_by = $1::uuid)::int`, userID).
		Scan(&o.Classes, &o.Tests, &o.Questions, &o.Groups, &o.Sets, &o.Media, &o.Assignments)
	if err != nil {
		w.t.Fatal(err)
	}
	return map[string]float64{
		"classes": float64(o.Classes), "tests": float64(o.Tests), "questions": float64(o.Questions),
		"questionGroups": float64(o.Groups), "questionSets": float64(o.Sets), "media": float64(o.Media), "assignments": float64(o.Assignments),
	}
}

func (w *world) studentCreatedBy(teacher string) string {
	w.t.Helper()
	var id string
	if err := w.pool.QueryRow(context.Background(), `
		INSERT INTO app.users (email, full_name, role_id, created_by)
		VALUES ('mo-coi-' || gen_random_uuid() || '@example.test', 'Học sinh chưa vào lớp', (SELECT id FROM app.roles WHERE builtin_key = 'student'), $1::uuid)
		RETURNING id::text`, teacher).Scan(&id); err != nil {
		w.t.Fatal(err)
	}
	w.t.Cleanup(func() {
		for _, sql := range []string{`DELETE FROM app.class_members WHERE user_id = $1::uuid`, `DELETE FROM app.users WHERE id = $1::uuid`} {
			if _, err := w.pool.Exec(context.Background(), sql, id); err != nil {
				w.t.Errorf("cleanup %q: %v", sql, err)
			}
		}
	})
	return id
}

func lists(t *testing.T, c *client, path, id string) bool {
	t.Helper()
	body := c.must(http.StatusOK, http.MethodGet, path, nil)
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Contains(string(raw), id)
}

func TestADisabledTeachersContentMovesToAnotherTeacherWhoListsAndEditsItWhileStudentsKeepTheirWork(t *testing.T) {
	w := bootWithStorage(t)
	a := w.teacherWorld("chuyen")
	adminEmail, adminPassword := w.createStaff("admin")
	admin := w.signedIn(adminEmail, adminPassword)
	heirEmail, heirPassword := w.createStaff("teacher")
	heir := w.signedIn(heirEmail, heirPassword)
	heirID := w.userID(heirEmail)
	thirdEmail, thirdPassword := w.createStaff("teacher")
	third := w.signedIn(thirdEmail, thirdPassword)
	path := "/admin/users/" + a.userID + "/transfer-content"
	var attemptsBefore int
	if err := w.pool.QueryRow(context.Background(), `
		SELECT count(*)::int FROM app.attempts t JOIN app.assignments s ON s.id = t.assignment_id WHERE s.created_by = $1::uuid`, a.userID).Scan(&attemptsBefore); err != nil {
		t.Fatal(err)
	}

	if status, body := admin.call(http.MethodPost, path, map[string]any{"toUserId": heirID}); status != http.StatusConflict || errorCode(body) != "USER_NOT_DISABLED" {
		t.Fatalf("moving an active account's content: %d %v, want 409 USER_NOT_DISABLED", status, body)
	}
	thirdID := w.userID(thirdEmail)
	setShare := a.grant(a.c, "question_set", a.ids["question-set"], thirdID, "use")
	toHeir := map[string]string{
		a.grant(a.c, "test", a.ids["test"], heirID, "edit"):                "test " + a.ids["test"],
		a.grant(a.c, "question_set", a.ids["question-set"], heirID, "use"): "question_set " + a.ids["question-set"],
	}
	following := w.grantRows(a.ids["share"], setShare, a.ids["shared-with-me"])
	admin.must(http.StatusOK, http.MethodPost, "/admin/users/"+a.userID+"/disable", nil)
	for label, to := range map[string]string{
		"the account itself": a.userID, "a student": a.ids["student"], "nobody": "01935000-0000-7000-8000-00000000fffe",
	} {
		if status, body := admin.call(http.MethodPost, path, map[string]any{"toUserId": to}); status != http.StatusUnprocessableEntity || errorCode(body) != "INVALID_RECIPIENT" {
			t.Errorf("moving to %s: %d %v, want 422 INVALID_RECIPIENT", label, status, body)
		}
	}
	if status, _ := heir.call(http.MethodPost, path, map[string]any{"toUserId": heirID}); status != http.StatusForbidden {
		t.Errorf("a teacher moving content answered %d, want 403", status)
	}

	owned, live := w.ownedBy(a.userID), w.liveOwnedBy(a.userID)
	var unfinished float64
	if err := w.pool.QueryRow(context.Background(), `
		SELECT count(*)::float8 FROM app.word_imports WHERE created_by = $1::uuid AND status NOT IN ('committed', 'cancelled')`, a.userID).Scan(&unfinished); err != nil {
		t.Fatal(err)
	}
	orphan := w.studentCreatedBy(a.userID)
	result := admin.must(http.StatusOK, http.MethodPost, path, map[string]any{"toUserId": heirID, "notify": true})

	moved, _ := result["moved"].(map[string]any)
	for key, n := range live {
		if moved[key] != n {
			t.Errorf("moved.%s = %v, want %v: %v", key, moved[key], n, result)
		}
	}
	if left, _ := result["leftBehind"].(map[string]any); left["imports"] != unfinished || unfinished < 1 {
		t.Errorf("leftBehind = %v, want the %v unfinished imports", left, unfinished)
	}
	if left, _ := result["leftBehind"].(map[string]any); left["students"] != float64(1) {
		t.Errorf("leftBehind = %v, want the one student the account created in no class", left)
	}
	if status, _ := heir.call(http.MethodGet, "/teacher/students/"+orphan, nil); status != http.StatusNotFound {
		t.Errorf("the recipient reading the student left behind: %d, want 404", status)
	}
	if after := w.ownedBy(a.userID); after != (ownedRows{}) {
		t.Errorf("the disabled account still owns %+v", after)
	}
	if got := w.ownedBy(heirID); got != owned {
		t.Errorf("the recipient owns %+v, want everything the account owned, deleted rows too: %+v", got, owned)
	}
	var stillMadeBy int
	if err := w.pool.QueryRow(context.Background(), `
		SELECT (SELECT count(*) FROM app.tests WHERE created_by = $1::uuid) + (SELECT count(*) FROM app.questions WHERE created_by = $1::uuid)
		     + (SELECT count(*) FROM app.media_assets WHERE uploaded_by = $1::uuid)`, a.userID).Scan(&stillMadeBy); err != nil {
		t.Fatal(err)
	}
	if stillMadeBy == 0 {
		t.Error("the transfer rewrote who made the rows; provenance must stay")
	}
	w.grantsFollowTheTransfer(a, heir, third, heirID, w.userID(adminEmail), toHeir, following)

	var audited string
	if err := w.pool.QueryRow(context.Background(), `
		SELECT diff->'owner'->>'new' FROM app.audit_log WHERE action = 'user.content_transferred' AND entity_id = $1::uuid`, a.userID).Scan(&audited); err != nil || audited != heirID {
		t.Errorf("audit row names %q (%v), want the recipient", audited, err)
	}

	for p, id := range map[string]string{
		"/teacher/tests": a.ids["test"], "/teacher/questions": a.ids["question"], "/teacher/question-groups": a.ids["question-group"],
		"/teacher/media": a.ids["media"], "/teacher/classes": a.ids["class"], "/teacher/assignments": a.ids["assignment"],
	} {
		if !lists(t, heir, p, id) {
			t.Errorf("the recipient's %s does not hold %s", p, id)
		}
		if lists(t, third, p, id) {
			t.Errorf("a third teacher's %s holds %s", p, id)
		}
	}
	test := heir.must(http.StatusOK, http.MethodGet, "/teacher/tests/"+a.ids["test"], nil)
	heir.must(http.StatusOK, http.MethodPatch, "/teacher/tests/"+a.ids["test"], map[string]any{
		"expectedUpdatedAt": test["updatedAt"], "title": "Đề đã nhận " + nonce(t),
	})
	heir.must(http.StatusOK, http.MethodGet, "/teacher/attempts/"+a.ids["attempt"], nil)
	for _, p := range []string{"/teacher/tests/" + a.ids["test"], "/teacher/classes/" + a.ids["class"], "/teacher/attempts/" + a.ids["attempt"]} {
		if status, _ := third.call(http.MethodGet, p, nil); status != http.StatusNotFound {
			t.Errorf("a third teacher reading %s: %d, want 404", p, status)
		}
	}

	a.student.c.must(http.StatusOK, http.MethodGet, "/app/assignments/"+a.student.ids["assignment"], nil)
	var attemptsAfter int
	if err := w.pool.QueryRow(context.Background(), `
		SELECT count(*)::int FROM app.attempts t JOIN app.assignments s ON s.id = t.assignment_id WHERE s.created_by = $1::uuid`, heirID).Scan(&attemptsAfter); err != nil {
		t.Fatal(err)
	}
	if attemptsAfter != attemptsBefore || attemptsBefore == 0 {
		t.Errorf("the moved assignments hold %d attempts, want the %d they held", attemptsAfter, attemptsBefore)
	}

	if !lists(t, heir, "/me/notifications", "content.transferred") {
		t.Error("the recipient's bell holds no content.transferred notification")
	}
	if _, err := w.pool.Exec(context.Background(), `INSERT INTO app.class_members (class_id, user_id, joined_via) VALUES ($1::uuid, $2::uuid, 'admin')`, a.ids["class"], orphan); err != nil {
		t.Fatal(err)
	}
	again := admin.must(http.StatusOK, http.MethodPost, path, map[string]any{"toUserId": heirID})
	if left, _ := again["leftBehind"].(map[string]any); left["students"] != float64(0) {
		t.Errorf("leftBehind = %v once the student is in a moved class, want no student", left)
	}
	if status, body := admin.call(http.MethodPost, path, map[string]any{"toUserId": w.userID(adminEmail)}); status != http.StatusOK {
		t.Errorf("moving to an Admin, who holds the teacher workspace: %d %v, want 200", status, body)
	}
}

func (p *party) grant(sharer *client, kind, resource, grantee, level string) string {
	p.c.w.t.Helper()
	made := sharer.must(http.StatusCreated, http.MethodPost, "/teacher/shares", map[string]any{
		"kind": kind, "resourceId": resource, "granteeIds": []string{grantee}, "access": level, "notify": false,
	})
	grants, _ := made["grants"].([]any)
	if len(grants) != 1 {
		p.c.w.t.Fatalf("sharing %s %s with %s made %v", kind, resource, grantee, made)
	}
	return id(grants[0].(map[string]any))
}

func (w *world) grantRows(ids ...string) []string {
	w.t.Helper()
	rows, err := w.pool.Query(context.Background(), `SELECT row_to_json(g)::text FROM app.content_shares g WHERE g.id = ANY($1::uuid[]) ORDER BY g.id`, ids)
	if err != nil {
		w.t.Fatal(err)
	}
	out, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		w.t.Fatal(err)
	}
	if len(out) != len(ids) {
		w.t.Fatalf("%d of the grants %v exist", len(out), ids)
	}
	return out
}

// grantsFollowTheTransfer checks what the transfer did to the grants of the
// account's content: the grants to the recipient, who now owns the test and
// the set, are gone, each audited as share.revoked by the Admin; the grants
// to others (A's colleague's to the test, the third teacher's to the set)
// are the same rows, unwritten, and now open the recipient's content; and the
// grant the account itself holds to its colleague's test stays, as a disabled
// grantee's grants do.
func (w *world) grantsFollowTheTransfer(a *party, heir, third *client, heirID, adminID string, toHeir map[string]string, following []string) {
	t := w.t
	t.Helper()
	ids := make([]string, 0, len(toHeir))
	for share := range toHeir {
		ids = append(ids, share)
	}
	var left int
	if err := w.pool.QueryRow(context.Background(), `SELECT count(*)::int FROM app.content_shares WHERE id = ANY($1::uuid[])`, ids).Scan(&left); err != nil || left != 0 {
		t.Errorf("%d grants to the recipient of what it now owns remain (%v)", left, err)
	}
	for _, resource := range toHeir {
		var n int
		if err := w.pool.QueryRow(context.Background(), `
			SELECT count(*)::int FROM app.audit_log
			 WHERE action = 'share.revoked' AND actor_user_id = $1::uuid AND entity || ' ' || entity_id::text = $2
			   AND diff->'shared_with'->'old'->>'granteeId' = $3`, adminID, resource, heirID).Scan(&n); err != nil || n != 1 {
			t.Errorf("the recipient's grant to %s is audited %d times (%v), want once, as share.revoked by the Admin", resource, n, err)
		}
	}
	var rowIDs []string
	for _, row := range following {
		var g struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal([]byte(row), &g); err != nil {
			t.Fatal(err)
		}
		rowIDs = append(rowIDs, g.ID)
	}
	if after := w.grantRows(rowIDs...); strings.Join(after, "\n") != strings.Join(following, "\n") {
		t.Errorf("the grants that follow their resource were written:\nbefore %v\nafter  %v", following, after)
	}

	for label, read := range map[string]func() sent{
		"A's colleague reading the test":    func() sent { return a.mate.send(http.MethodGet, "/teacher/tests/"+a.ids["test"], nil) },
		"the third teacher reading the set": func() sent { return third.send(http.MethodGet, "/teacher/question-sets/"+a.ids["question-set"], nil) },
	} {
		got := read()
		owner, _ := got.json["owner"].(map[string]any)
		if got.status != http.StatusOK || got.json["access"] != "use" || owner["id"] != heirID {
			t.Errorf("%s after the transfer: %s, want 200 with access use and the recipient as its owner", label, answer(got))
		}
	}
	shared := third.must(http.StatusOK, http.MethodGet, "/teacher/shared-with-me", nil)
	items, _ := shared["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["resourceId"] != a.ids["question-set"] || items[0].(map[string]any)["owner"].(map[string]any)["id"] != heirID {
		t.Errorf("the third teacher's Shared with me after the transfer reads %v, want the set, owned by the recipient", items)
	}
	if mine := heir.must(http.StatusOK, http.MethodGet, "/teacher/shared-with-me", nil); mine["total"] != float64(0) {
		t.Errorf("the recipient's Shared with me still lists %v: it owns them now", mine["items"])
	}
	people := heir.must(http.StatusOK, http.MethodGet, "/teacher/shares?kind=test&resourceId="+a.ids["test"], nil)
	grants, _ := people["grants"].([]any)
	if len(grants) != 1 || id(grants[0].(map[string]any)) != a.ids["share"] || people["owner"].(map[string]any)["id"] != heirID {
		t.Errorf("the recipient's People with access to the test reads %v, want the colleague's grant alone under the recipient", people)
	}
}
