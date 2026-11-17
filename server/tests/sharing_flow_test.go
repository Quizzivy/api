//go:build e2e

package e2e

import (
	"context"
	"net/http"
	"slices"
	"testing"
)

func (c *client) draftWith(title string, questions ...string) string {
	c.w.t.Helper()
	questions = append([]string{}, questions...)
	test := c.must(http.StatusCreated, http.MethodPost, "/teacher/tests", map[string]any{"title": title})
	c.must(http.StatusOK, http.MethodPatch, "/teacher/tests/"+id(test), map[string]any{
		"expectedUpdatedAt": test["updatedAt"],
		"sections":          []map[string]any{{"title": "Phần 1", "questionIds": questions}},
	})
	return id(test)
}

func (c *client) draftQuestions(test string) []any {
	c.w.t.Helper()
	sections := c.must(http.StatusOK, http.MethodGet, "/teacher/tests/"+test, nil)["sections"].([]any)
	return sections[0].(map[string]any)["questionIds"].([]any)
}

func (c *client) shares(kind, resource string, level string, grantees ...string) map[string]any {
	c.w.t.Helper()
	return c.must(http.StatusCreated, http.MethodPost, "/teacher/shares", map[string]any{
		"kind": kind, "resourceId": resource, "granteeIds": grantees, "access": level, "notify": false,
	})
}

func onlyGrant(t *testing.T, made map[string]any) string {
	t.Helper()
	grants, _ := made["grants"].([]any)
	if len(grants) != 1 {
		t.Fatalf("the share made %v, want one grant", made)
	}
	return id(grants[0].(map[string]any))
}

func (w *world) auditCount(action, entityID, actorID string) int {
	w.t.Helper()
	var n int
	if err := w.pool.QueryRow(context.Background(), `
		SELECT count(*)::int FROM app.audit_log WHERE action = $1 AND entity_id = $2::uuid AND actor_user_id = $3::uuid`, action, entityID, actorID).Scan(&n); err != nil {
		w.t.Fatal(err)
	}
	return n
}

// TestASharedTestIsCoEditedWithAForkPublishedRevokedAndCopied is the share
// round trip through HTTP. A shares a test with a colleague as Can edit and
// the colleague is told. The colleague edits a question A also uses in a
// draft that is not shared, which forks it: the shared draft now uses A's new
// question, made by the colleague, and the other draft is untouched. The
// colleague publishes, which tells A, assigns the version to their own
// class, and makes a copy of the test. Stop sharing then closes the test to
// the colleague, while their assignment and their copy, which is theirs and
// independent, keep working.
func TestASharedTestIsCoEditedWithAForkPublishedRevokedAndCopied(t *testing.T) {
	w := bootWithStorage(t)
	owner := w.signedIn(w.createStaff("teacher"))
	ownerID := owner.me(t)
	colleague := w.signedIn(w.createStaff("teacher"))
	colleagueID := colleague.me(t)

	question := id(owner.must(http.StatusCreated, http.MethodPost, "/teacher/questions", plainQuestion("Câu dùng ở hai đề")))
	shared := owner.draftWith("Đề soạn chung "+nonce(t), question)
	unshared := owner.draftWith("Đề không chia sẻ "+nonce(t), question)

	share := onlyGrant(t, owner.must(http.StatusCreated, http.MethodPost, "/teacher/shares", map[string]any{
		"kind": "test", "resourceId": shared, "granteeIds": []string{colleagueID}, "access": "edit", "notify": true, "message": "Cùng soạn nhé",
	}))
	if !lists(t, colleague, "/me/notifications", "content.shared") {
		t.Error("the colleague was not told of the share")
	}
	inbox := colleague.must(http.StatusOK, http.MethodGet, "/teacher/shared-with-me", nil)["items"].([]any)
	if len(inbox) != 1 || inbox[0].(map[string]any)["resourceId"] != shared || inbox[0].(map[string]any)["access"] != "edit" {
		t.Fatalf("the colleague's Shared with me reads %v, want the test at Can edit", inbox)
	}

	edited := colleague.must(http.StatusOK, http.MethodPatch, "/teacher/tests/"+shared+"/questions/"+question, plainQuestion("Câu đồng nghiệp đã sửa"))
	fork := id(edited["question"].(map[string]any))
	if edited["forked"] != true || fork == question {
		t.Fatalf("the colleague's edit of a question another draft uses answered forked %v with %s, want a fork", edited["forked"], fork)
	}
	if got := owner.draftQuestions(shared); !slices.Equal(got, []any{fork}) {
		t.Errorf("the shared draft holds %v, want the fork %s", got, fork)
	}
	if got := owner.draftQuestions(unshared); !slices.Equal(got, []any{question}) {
		t.Errorf("the draft that was never shared holds %v, want the original %s", got, question)
	}
	if prompt := owner.must(http.StatusOK, http.MethodGet, "/teacher/questions/"+question, nil)["prompt"]; prompt != "Câu dùng ở hai đề" {
		t.Errorf("the original question reads %q after the colleague's edit, want it unchanged", prompt)
	}
	var forkOwner, forkMaker string
	if err := w.pool.QueryRow(context.Background(), `SELECT owner_id::text, created_by::text FROM app.questions WHERE id = $1::uuid`, fork).Scan(&forkOwner, &forkMaker); err != nil {
		t.Fatal(err)
	}
	if forkOwner != ownerID || forkMaker != colleagueID {
		t.Errorf("the fork is owned by %s and made by %s, want the test's owner and the colleague", forkOwner, forkMaker)
	}

	version := id(colleague.must(http.StatusCreated, http.MethodPost, "/teacher/tests/"+shared+"/publish", nil))
	if !lists(t, owner, "/me/notifications", "test.published_by_collaborator") {
		t.Error("the owner was not told of the colleague's publish")
	}
	class := colleague.class("Lớp của đồng nghiệp " + nonce(t))
	learnerEmail := "hoc-chung-" + nonce(t) + "@example.com"
	learnerMade := colleague.must(http.StatusCreated, http.MethodPost, "/teacher/students", map[string]any{
		"email": learnerEmail, "fullName": "Học viên của đồng nghiệp", "classIds": []string{class},
	})
	assignment := id(colleague.assign(version, class))
	learner := w.settledIn(learnerEmail, learnerMade["temporaryPassword"].(string))
	sitting := learner.must(http.StatusOK, http.MethodPost, "/app/assignments/"+assignment+"/attempts", nil)
	attempt := id(sitting["attempt"].(map[string]any))
	learner.must(http.StatusOK, http.MethodPost, "/app/attempts/"+attempt+"/submit", map[string]any{"sessionId": sitting["sessionId"], "reason": "manual"})

	copied := colleague.must(http.StatusCreated, http.MethodPost, "/teacher/tests/"+shared+"/copy", nil)
	mine := id(copied)
	if copied["access"] != "owner" || copied["owner"].(map[string]any)["id"] != colleagueID || copied["status"] != "draft" {
		t.Errorf("the copy reads access %v, owner %v, status %v, want the colleague's own draft", copied["access"], copied["owner"], copied["status"])
	}
	copyQuestions := colleague.draftQuestions(mine)
	if len(copyQuestions) != 1 || copyQuestions[0] == fork || copyQuestions[0] == question {
		t.Errorf("the copy holds %v, want a question of the colleague's own", copyQuestions)
	}

	owner.must(http.StatusNoContent, http.MethodDelete, "/teacher/shares/"+share, nil)
	for label, got := range map[string]sent{
		"reading the test":  colleague.send(http.MethodGet, "/teacher/tests/"+shared, nil),
		"copying it again":  colleague.send(http.MethodPost, "/teacher/tests/"+shared+"/copy", nil),
		"editing its draft": colleague.send(http.MethodPatch, "/teacher/tests/"+shared+"/questions/"+fork, new(jsonPayload(t, plainQuestion("Sửa sau khi ngừng")))),
	} {
		if got.status != http.StatusNotFound {
			t.Errorf("the colleague %s after Stop sharing: %s, want 404", label, answer(got))
		}
	}
	if total := colleague.must(http.StatusOK, http.MethodGet, "/teacher/shared-with-me", nil)["total"]; total != float64(0) {
		t.Errorf("the colleague's Shared with me counts %v after Stop sharing", total)
	}
	for label, path := range map[string]string{
		"monitor": "/teacher/assignments/" + assignment + "/attempts", "review": "/teacher/attempts/" + attempt,
		"grading queue": "/teacher/grading/queue?assignmentId=" + assignment, "results": "/teacher/assignments/results.csv?ids=" + assignment,
	} {
		if got := colleague.send(http.MethodGet, path, nil); !success(got) {
			t.Errorf("the colleague's %s of their assignment after Stop sharing: %s", label, answer(got))
		}
	}
	if got := learner.send(http.MethodGet, "/app/attempts/"+attempt+"/result", nil); !success(got) {
		t.Errorf("the student's result after Stop sharing: %s", answer(got))
	}

	draft := colleague.must(http.StatusOK, http.MethodGet, "/teacher/tests/"+mine, nil)
	colleague.must(http.StatusOK, http.MethodPatch, "/teacher/tests/"+mine, map[string]any{"expectedUpdatedAt": draft["updatedAt"], "title": "Bản sao đã sửa " + nonce(t)})
	colleague.must(http.StatusOK, http.MethodPatch, "/teacher/questions/"+copyQuestions[0].(string), plainQuestion("Câu của bản sao đã sửa"))
	if prompt := owner.must(http.StatusOK, http.MethodGet, "/teacher/questions/"+fork, nil)["prompt"]; prompt != "Câu đồng nghiệp đã sửa" {
		t.Errorf("the owner's question reads %q after the colleague edited their copy, want it unchanged", prompt)
	}

	for _, row := range []struct {
		action, entity, actor string
	}{
		{"share.granted", shared, ownerID}, {"question.forked", fork, colleagueID}, {"share.copied", mine, colleagueID}, {"share.revoked", shared, ownerID},
	} {
		if n := w.auditCount(row.action, row.entity, row.actor); n != 1 {
			t.Errorf("%s on %s by %s is audited %d times, want once", row.action, row.entity, row.actor, n)
		}
	}
}

// TestSharingRefusesEachEscalation: a teacher whose role lacks content.share
// is refused every share operation by the permission middleware yet still
// reads what is shared with them; a grantee, at either level, can neither
// raise nor stop their own grant, nor share further, nor read who else holds
// one; and nobody, the owner and an Admin included, can grant to a student.
func TestSharingRefusesEachEscalation(t *testing.T) {
	w := bootWithStorage(t)
	owner := w.signedIn(w.createStaff("teacher"))
	admin := w.signedIn(w.createStaff("admin"))
	grantee := w.signedIn(w.createStaff("teacher"))
	granteeID := grantee.me(t)
	bystanderID := w.signedIn(w.createStaff("teacher")).me(t)
	test := owner.draftWith("Đề không chia sẻ tiếp " + nonce(t))
	set := id(owner.must(http.StatusCreated, http.MethodPost, "/teacher/question-sets", map[string]any{
		"name": "Bộ không chia sẻ tiếp", "questionIds": []string{id(owner.must(http.StatusCreated, http.MethodPost, "/teacher/questions", plainQuestion("Câu trong bộ")))},
	}))

	var keys []string
	rows, err := w.pool.Query(context.Background(), `
		SELECT rp.permission_key FROM app.role_permissions rp JOIN app.roles r ON r.id = rp.role_id
		 WHERE r.builtin_key = 'teacher' AND rp.permission_key <> 'content.share'`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			t.Fatal(err)
		}
		keys = append(keys, key)
	}
	rows.Close()
	unshared := w.signedIn(w.staffHolding(keys...))
	own := unshared.draftWith("Đề của người không được chia sẻ " + nonce(t))
	given := onlyGrant(t, owner.shares("test", test, "use", unshared.me(t)))
	for label, got := range map[string]sent{
		"sharing their own test": unshared.send(http.MethodPost, "/teacher/shares", new(jsonPayload(t, map[string]any{
			"kind": "test", "resourceId": own, "granteeIds": []string{bystanderID}, "access": "use", "notify": false,
		}))),
		"reading who holds their own test": unshared.send(http.MethodGet, "/teacher/shares?kind=test&resourceId="+own, nil),
		"changing a grant":                 unshared.send(http.MethodPatch, "/teacher/shares/"+given, new(jsonPayload(t, map[string]any{"access": "edit"}))),
		"stopping a grant":                 unshared.send(http.MethodDelete, "/teacher/shares/"+given, nil),
		"searching the directory":          unshared.send(http.MethodGet, "/teacher/directory/teachers?q=co", nil),
	} {
		if got.status != http.StatusForbidden || errorCodeOf(got) != "FORBIDDEN" {
			t.Errorf("a teacher without content.share %s: %s, want the middleware's 403 FORBIDDEN", label, answer(got))
		}
	}
	if got := unshared.send(http.MethodGet, "/teacher/tests/"+test, nil); got.status != http.StatusOK || got.json["access"] != "use" {
		t.Errorf("a teacher without content.share reading a test shared with them: %s", answer(got))
	}
	if items := unshared.must(http.StatusOK, http.MethodGet, "/teacher/shared-with-me", nil)["items"].([]any); len(items) != 1 {
		t.Errorf("a teacher without content.share lists %v under Shared with me, want the one grant", items)
	}

	for _, level := range []string{"use", "edit"} {
		grants := map[string]string{
			"test":         onlyGrant(t, owner.shares("test", test, level, granteeID)),
			"question_set": onlyGrant(t, owner.shares("question_set", set, level, granteeID)),
		}
		resources := map[string]string{"test": test, "question_set": set}
		for kind, grant := range grants {
			raise := "edit"
			if level == "edit" {
				raise = "use"
			}
			for label, got := range map[string]sent{
				"changing their own grant": grantee.send(http.MethodPatch, "/teacher/shares/"+grant, new(jsonPayload(t, map[string]any{"access": raise}))),
				"stopping their own grant": grantee.send(http.MethodDelete, "/teacher/shares/"+grant, nil),
				"sharing it further": grantee.send(http.MethodPost, "/teacher/shares", new(jsonPayload(t, map[string]any{
					"kind": kind, "resourceId": resources[kind], "granteeIds": []string{bystanderID}, "access": "use", "notify": false,
				}))),
				"reading who holds it": grantee.send(http.MethodGet, "/teacher/shares?kind="+kind+"&resourceId="+resources[kind], nil),
			} {
				if got.status != http.StatusForbidden || errorCodeOf(got) != "SHARE_OWNER_ONLY" {
					t.Errorf("a %s grantee of the %s %s: %s, want 403 SHARE_OWNER_ONLY", level, kind, label, answer(got))
				}
			}
			people := owner.must(http.StatusOK, http.MethodGet, "/teacher/shares?kind="+kind+"&resourceId="+resources[kind], nil)["grants"].([]any)
			at := slices.IndexFunc(people, func(g any) bool { return id(g.(map[string]any)) == grant })
			if at < 0 || people[at].(map[string]any)["access"] != level || len(people) != 1+btoi(kind == "test") {
				t.Errorf("after the refusals, People with access to the %s reads %v, want the %s grant unchanged and no new one", kind, people, level)
			}
			owner.must(http.StatusNoContent, http.MethodDelete, "/teacher/shares/"+grant, nil)
		}
	}

	learnerEmail := "khong-nhan-" + nonce(t) + "@example.com"
	made := owner.must(http.StatusCreated, http.MethodPost, "/teacher/students", map[string]any{
		"email": learnerEmail, "fullName": "Học viên không nhận chia sẻ", "classIds": []string{owner.class("Lớp " + nonce(t))},
	})
	student := made["user"].(map[string]any)["id"].(string)
	learner := w.settledIn(learnerEmail, made["temporaryPassword"].(string))
	for who, c := range map[string]*client{"the owner": owner, "an Admin": admin} {
		for kind, resource := range map[string]string{"test": test, "question_set": set} {
			got := c.must(http.StatusCreated, http.MethodPost, "/teacher/shares", map[string]any{
				"kind": kind, "resourceId": resource, "granteeIds": []string{student}, "access": "edit", "notify": true,
			})
			skipped, _ := got["skipped"].([]any)
			if grants, _ := got["grants"].([]any); len(grants) != 0 || len(skipped) != 1 ||
				skipped[0].(map[string]any)["userId"] != student || skipped[0].(map[string]any)["code"] != "NOT_A_TEACHER" {
				t.Errorf("%s sharing the %s with a student made %v, want no grant and NOT_A_TEACHER", who, kind, got)
			}
		}
	}
	var held int
	if err := w.pool.QueryRow(context.Background(), `SELECT count(*)::int FROM app.content_shares WHERE grantee_id = $1::uuid`, student).Scan(&held); err != nil || held != 0 {
		t.Errorf("the student holds %d grants (%v)", held, err)
	}
	for _, path := range []string{"/teacher/shared-with-me", "/teacher/tests/" + test, "/teacher/question-sets/" + set} {
		if got := learner.send(http.MethodGet, path, nil); got.status != http.StatusForbidden {
			t.Errorf("the student reading %s: %s, want 403", path, answer(got))
		}
	}
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

// TestADisabledAccountsGrantsStay: while the owner is disabled, what they
// shared keeps working at its level and Shared with me says the owner's
// account is disabled; while a grantee is disabled, their grants stay, so
// they find everything again once re-enabled.
func TestADisabledAccountsGrantsStay(t *testing.T) {
	w := bootWithStorage(t)
	admin := w.signedIn(w.createStaff("admin"))
	ownerEmail, ownerPassword := w.createStaff("teacher")
	owner := w.signedIn(ownerEmail, ownerPassword)
	ownerID := owner.me(t)
	granteeEmail, granteePassword := w.createStaff("teacher")
	grantee := w.signedIn(granteeEmail, granteePassword)
	granteeID := grantee.me(t)
	question := id(owner.must(http.StatusCreated, http.MethodPost, "/teacher/questions", plainQuestion("Câu của người sẽ bị khoá")))
	test := owner.draftWith("Đề của người sẽ bị khoá "+nonce(t), question)
	set := id(owner.must(http.StatusCreated, http.MethodPost, "/teacher/question-sets", map[string]any{"name": "Bộ của người sẽ bị khoá", "questionIds": []string{question}}))
	grants := []string{onlyGrant(t, owner.shares("test", test, "edit", granteeID)), onlyGrant(t, owner.shares("question_set", set, "use", granteeID))}
	inbox := func(c *client, disabled bool) {
		t.Helper()
		items := c.must(http.StatusOK, http.MethodGet, "/teacher/shared-with-me", nil)["items"].([]any)
		if len(items) != 2 {
			t.Fatalf("Shared with me lists %v, want the test and the set", items)
		}
		for _, item := range items {
			row := item.(map[string]any)
			if who := row["owner"].(map[string]any); who["id"] != ownerID || who["disabled"] != disabled {
				t.Errorf("Shared with me's %s names its owner %v, want disabled %v", row["kind"], who, disabled)
			}
		}
	}
	works := func(c *client, when string) {
		t.Helper()
		got := c.send(http.MethodGet, "/teacher/tests/"+test, nil)
		if got.status != http.StatusOK || got.json["access"] != "edit" {
			t.Fatalf("the grantee reading the test %s: %s", when, answer(got))
		}
		if renamed := c.send(http.MethodPatch, "/teacher/tests/"+test, new(jsonPayload(t, map[string]any{
			"expectedUpdatedAt": got.json["updatedAt"], "title": "Đề sửa " + when + " " + nonce(t),
		}))); !success(renamed) {
			t.Errorf("the edit grantee renaming the test %s: %s", when, answer(renamed))
		}
		if read := c.send(http.MethodGet, "/teacher/question-sets/"+set, nil); read.status != http.StatusOK || read.json["access"] != "use" {
			t.Errorf("the grantee reading the set %s: %s", when, answer(read))
		}
		if !lists(t, c, "/teacher/questions?setId="+set, question) {
			t.Errorf("the grantee listing the set's questions %s does not find its question", when)
		}
	}

	admin.must(http.StatusOK, http.MethodPost, "/admin/users/"+ownerID+"/disable", nil)
	works(grantee, "while its owner is disabled")
	inbox(grantee, true)
	if got := grantee.send(http.MethodPost, "/teacher/tests/"+test+"/copy", nil); got.status != http.StatusCreated {
		t.Errorf("copying a disabled owner's test: %s", answer(got))
	}
	admin.must(http.StatusOK, http.MethodPost, "/admin/users/"+ownerID+"/enable", nil)
	inbox(grantee, false)

	admin.must(http.StatusOK, http.MethodPost, "/admin/users/"+granteeID+"/disable", nil)
	if got := grantee.send(http.MethodGet, "/teacher/tests/"+test, nil); got.status != http.StatusUnauthorized {
		t.Errorf("a disabled grantee's session reading the test: %s, want 401", answer(got))
	}
	owner = w.signedIn(ownerEmail, ownerPassword)
	for kind, resource := range map[string]string{"test": test, "question_set": set} {
		people := owner.must(http.StatusOK, http.MethodGet, "/teacher/shares?kind="+kind+"&resourceId="+resource, nil)["grants"].([]any)
		if len(people) != 1 || !slices.Contains(grants, id(people[0].(map[string]any))) || people[0].(map[string]any)["grantee"].(map[string]any)["disabled"] != true {
			t.Errorf("People with access to the %s while its grantee is disabled reads %v, want the grant, its grantee flagged", kind, people)
		}
	}
	admin.must(http.StatusOK, http.MethodPost, "/admin/users/"+granteeID+"/enable", nil)
	again := w.signedIn(granteeEmail, granteePassword)
	works(again, "once the grantee is enabled again")
	inbox(again, false)
}
