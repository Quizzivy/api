//go:build e2e

package e2e

import (
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// What a reader at a level of a grant gets from an operation that needs
// less than that level is the operation's own answer; the levels below are
// what TestEachSharingOperationAnswersAtEachLevelOfAGrant expects when it
// needs more. needsBind is binding A's file, which a share and a copy open.
const (
	needsBind  = "bind"
	needsNever = "never"
)

// walkedElsewhere names the operations on a shared test that
// TestAGrantOpensEachOfATestsOperationsAtItsLevel walks at each level, and the
// one operation that names no resource.
var walkedElsewhere = map[string]string{
	"createQuestion":         "share_access_flow: createQuestion forTestId",
	"duplicateQuestion":      "share_access_flow: duplicateQuestion, with and without forTestId",
	"searchTeacherDirectory": "names no resource: every teacher reads the same directory",
	"listQuestionGroups":     "share_access_flow: never lists the test's group at any level",
	"getQuestionGroup":       "share_access_flow: getQuestionGroup",
	"createQuestionGroup":    "share_access_flow: createQuestionGroup in the test",
	"updateQuestionGroup":    "share_access_flow: updateQuestionGroup of the test",
	"deleteQuestionGroup":    "share_access_flow: deleteQuestionGroup of the test",
	"copyQuestionGroup":      "share_access_flow: copyQuestionGroup to the caller's bank",
	"archiveQuestionGroup":   "share_access_flow: archiveQuestionGroup of the test, refused as the owner is",
	"moveTestQuestion":       "share_access_flow: moveTestQuestion within the test's group",
}

// sharingSurface is every operation R7's sharing added or gave a share's
// context: the question sets, the grants, Shared with me, the directory, the
// copies and the edit in a test's context, the bank's setId and forTestId,
// and the groups and moves a test's grant opens, since a test's group is read
// and changed as its test is (testAccess).
var sharingSurface = regexp.MustCompile(`^/teacher/(question-sets|shares|shared-with-me|directory|question-groups)(/|$)|^/teacher/tests/\{id\}/(copy|question-copies|questions/\{questionId\}|questions/move)$`)

// TestEachSharingOperationAnswersAtEachLevelOfAGrant walks every operation
// T-R7.2 … T-R7.7 added at each level A's colleague can hold to A's test and
// A's question set: with no grant (another teacher), with Can use, with Can
// edit, and after Stop sharing. A caller who cannot read gets exactly what a
// missing id answers; a Can use reader reads and copies, and is refused every
// write with SHARE_READ_ONLY; a Can edit reader writes, and is refused the
// owner's actions (adding to or deleting the set, and managing who it is
// shared with) with SHARE_OWNER_ONLY. Lists never name what the caller cannot
// read. A's files open to the colleague through the share, and stay open
// after the revocation through the copy the colleague made under it.
func TestEachSharingOperationAnswersAtEachLevelOfAGrant(t *testing.T) {
	w := bootWithStorage(t)
	a := w.teacherWorld("A")
	stranger := w.signedIn(w.createStaff("teacher"))
	mate := a.mate
	bystander := id(w.signedIn(w.createStaff("teacher")).must(http.StatusOK, http.MethodGet, "/auth/me", nil))
	test, set, question := a.id("test"), a.id("question-set"), a.id("question")
	testShare := a.id("share")
	setShare := a.grant(a.c, "question_set", set, a.mateID, "use")
	mine := id(mate.must(http.StatusCreated, http.MethodPost, "/teacher/questions", plainQuestion("Câu của đồng nghiệp")))

	// extra is a question of A's that only the set holds, made afresh at each
	// level, which the set's edits change and take out.
	var extra string
	q := func(v any) *payload { return new(jsonPayload(t, v)) }
	ops := []sharedOp{
		{"getQuestionSet", needsRead, set, func(id string) (string, string, *payload) {
			return http.MethodGet, "/teacher/question-sets/" + id, nil
		}},
		{"listQuestions setId", needsRead, set, func(id string) (string, string, *payload) {
			return http.MethodGet, "/teacher/questions?limit=100&setId=" + id, nil
		}},
		{"copyQuestionSet", needsRead, set, func(id string) (string, string, *payload) {
			return http.MethodPost, "/teacher/question-sets/" + id + "/copy", nil
		}},
		{"copyQuestionSet questionIds", needsRead, set, func(id string) (string, string, *payload) {
			return http.MethodPost, "/teacher/question-sets/" + id + "/copy", q(map[string]any{"questionIds": []string{question}})
		}},
		{"copyTest", needsRead, test, func(id string) (string, string, *payload) {
			return http.MethodPost, "/teacher/tests/" + id + "/copy", nil
		}},
		{"createQuestion with A's image", needsBind, a.id("media"), func(id string) (string, string, *payload) {
			body := plainQuestion("Câu dùng hình của A")
			body["mediaAssetId"] = id
			return http.MethodPost, "/teacher/questions", q(body)
		}},

		{"createQuestionSet with A's question", needsNever, question, func(id string) (string, string, *payload) {
			return http.MethodPost, "/teacher/question-sets", q(map[string]any{"name": "Bộ lấy câu của A", "questionIds": []string{id}})
		}},

		{"updateQuestionSet", needsEdit, set, func(id string) (string, string, *payload) {
			return http.MethodPatch, "/teacher/question-sets/" + id, q(map[string]any{"name": "Bộ đồng nghiệp đã sửa " + nonce(t)})
		}},
		{"updateQuestion setId", needsEdit, set, func(id string) (string, string, *payload) {
			body := plainQuestion("Đồng nghiệp sửa trong bộ")
			body["expectedUpdatedAt"] = a.c.questionRevision(extra)
			return http.MethodPatch, "/teacher/questions/" + extra + "?setId=" + id, q(body)
		}},
		{"removeQuestionSetItems", needsEdit, set, func(id string) (string, string, *payload) {
			return http.MethodPost, "/teacher/question-sets/" + id + "/items/remove", q(map[string]any{"questionIds": []string{extra}})
		}},
		{"editTestQuestion", needsEdit, test, func(id string) (string, string, *payload) {
			return http.MethodPatch, "/teacher/tests/" + id + "/questions/" + question, q(choiceInput(a))
		}},
		{"copyQuestionsForTest", needsEdit, test, func(id string) (string, string, *payload) {
			return http.MethodPost, "/teacher/tests/" + id + "/question-copies", q(map[string]any{"questionIds": []string{mine}})
		}},

		{"addQuestionSetItems", needsOwner, set, func(id string) (string, string, *payload) {
			return http.MethodPost, "/teacher/question-sets/" + id + "/items", q(map[string]any{"questionIds": []string{extra}})
		}},
		{"deleteQuestionSet", needsOwner, set, func(id string) (string, string, *payload) {
			return http.MethodDelete, "/teacher/question-sets/" + id, nil
		}},
		{"listShares test", needsOwner, test, func(id string) (string, string, *payload) {
			return http.MethodGet, "/teacher/shares?kind=test&resourceId=" + id, nil
		}},
		{"listShares question_set", needsOwner, set, func(id string) (string, string, *payload) {
			return http.MethodGet, "/teacher/shares?kind=question_set&resourceId=" + id, nil
		}},
		{"createShares test", needsOwner, test, func(id string) (string, string, *payload) {
			return http.MethodPost, "/teacher/shares", q(map[string]any{"kind": "test", "resourceId": id, "granteeIds": []string{bystander}, "access": "use", "notify": false})
		}},
		{"createShares question_set", needsOwner, set, func(id string) (string, string, *payload) {
			return http.MethodPost, "/teacher/shares", q(map[string]any{"kind": "question_set", "resourceId": id, "granteeIds": []string{bystander}, "access": "use", "notify": false})
		}},
		{"updateShare of the test's grant", needsOwner, testShare, func(id string) (string, string, *payload) {
			return http.MethodPatch, "/teacher/shares/" + id, q(map[string]any{"access": "edit"})
		}},
		{"updateShare of the set's grant", needsOwner, setShare, func(id string) (string, string, *payload) {
			return http.MethodPatch, "/teacher/shares/" + id, q(map[string]any{"access": "edit"})
		}},
		{"deleteShare of the test's grant", needsOwner, testShare, func(id string) (string, string, *payload) {
			return http.MethodDelete, "/teacher/shares/" + id, nil
		}},
		{"deleteShare of the set's grant", needsOwner, setShare, func(id string) (string, string, *payload) {
			return http.MethodDelete, "/teacher/shares/" + id, nil
		}},
	}
	covered := map[string]bool{"listQuestionSets": true, "listSharedWithMe": true, "removeSharedWithMe": true, "listQuestions": true, "updateQuestion": true}
	for _, op := range ops {
		covered[strings.Fields(op.name)[0]] = true
	}
	var unwalked []string
	for _, op := range scopedOperations(t) {
		if !sharingSurface.MatchString(op.path) && !slices.ContainsFunc(op.slots, func(s resourceSlot) bool { return s.Name == "setId" || s.Name == "forTestId" }) {
			continue
		}
		if !covered[op.id] && walkedElsewhere[op.id] == "" {
			unwalked = append(unwalked, op.id)
		}
	}
	sort.Strings(unwalked)
	if len(unwalked) > 0 {
		t.Fatalf("the grant walks leave out %v: give each its level", unwalked)
	}

	for _, level := range []string{"none", "use", "edit", "revoked"} {
		caller := mate
		switch level {
		case "none":
			caller = stranger
		case "edit":
			for _, share := range []string{testShare, setShare} {
				a.c.must(http.StatusOK, http.MethodPatch, "/teacher/shares/"+share, map[string]any{"access": "edit"})
			}
		case "revoked":
			for _, share := range []string{testShare, setShare} {
				if got := a.c.send(http.MethodDelete, "/teacher/shares/"+share, nil); !success(got) {
					t.Fatalf("stopping the share: %s", answer(got))
				}
			}
		}
		readable := level == "use" || level == "edit"
		extra = id(a.c.must(http.StatusCreated, http.MethodPost, "/teacher/questions", plainQuestion("Câu chỉ bộ giữ "+level)))
		a.c.must(http.StatusOK, http.MethodPost, "/teacher/question-sets/"+set+"/items", map[string]any{"questionIds": []string{extra}})

		for _, op := range ops {
			method, path, body := op.build(op.target)
			got := caller.send(method, path, body)
			where := op.name + " at " + level
			opens := (readable || (op.needs == needsBind && level == "revoked")) && op.needs != needsNever
			if !opens {
				method, path, body = op.build(uuid.NewString())
				missing := caller.send(method, path, body)
				if answer(got) != answer(missing) {
					t.Errorf("%s answered\n  %s\nwhere a missing id answers\n  %s", where, answer(got), answer(missing))
				}
				if success(got) {
					t.Errorf("%s succeeded: %s", where, answer(got))
				}
				continue
			}
			want := ""
			switch {
			case op.needs == needsOwner:
				want = "SHARE_OWNER_ONLY"
			case op.needs == needsEdit && level == "use":
				want = "SHARE_READ_ONLY"
			}
			if want == "" {
				if !success(got) {
					t.Errorf("%s: %s, want success", where, answer(got))
				}
				continue
			}
			failure, _ := got.json["error"].(map[string]any)
			code, _ := failure["code"].(string)
			if got.status != http.StatusForbidden || code != want {
				t.Errorf("%s: %s, want 403 %s", where, answer(got), want)
			}
		}

		if readable {
			read := caller.must(http.StatusOK, http.MethodGet, "/teacher/question-sets/"+set, nil)
			if read["access"] != level || read["owner"].(map[string]any)["id"] != a.userID || read["sharedWith"] != float64(0) {
				t.Errorf("the set at %s reads access %v, owner %v, sharedWith %v", level, read["access"], read["owner"], read["sharedWith"])
			}
		}
		shared := caller.must(http.StatusOK, http.MethodGet, "/teacher/shared-with-me?limit=100", nil)
		for kind, resource := range map[string]string{"test": test, "question_set": set} {
			items, _ := shared["items"].([]any)
			at := slices.IndexFunc(items, func(item any) bool { return item.(map[string]any)["resourceId"] == resource })
			if (at >= 0) != readable {
				t.Errorf("Shared with me at %s lists A's %s: %v", level, kind, at >= 0)
				continue
			}
			if at >= 0 {
				row := items[at].(map[string]any)
				if row["access"] != level || row["kind"] != kind || row["owner"].(map[string]any)["id"] != a.userID || row["owner"].(map[string]any)["disabled"] != false {
					t.Errorf("Shared with me's row for A's %s at %s reads %v", kind, level, row)
				}
			}
		}
		if lists(t, caller, "/teacher/question-sets", set) {
			t.Errorf("the caller's own sets at %s list A's set: a shared set is in Shared with me", level)
		}
		if lists(t, caller, "/teacher/questions?limit=100", extra) {
			t.Errorf("the caller's bank at %s lists A's question", level)
		}
	}

	// After the revocation the colleague's copies are theirs and stay: the
	// copy of A's test still binds A's file, which is why the colleague may
	// still bind it. A teacher who never held a grant or a copy may not.
	theirs, absent := stranger.send(http.MethodPost, "/teacher/questions", q(map[string]any{
		"type": "short_answer", "prompt": "Người lạ dùng hình của A", "points": 1, "sampleAnswer": "Không.", "mediaAssetId": a.id("media"),
	})), stranger.send(http.MethodPost, "/teacher/questions", q(map[string]any{
		"type": "short_answer", "prompt": "Người lạ dùng hình của A", "points": 1, "sampleAnswer": "Không.", "mediaAssetId": uuid.NewString(),
	}))
	if success(theirs) || answer(theirs) != answer(absent) {
		t.Errorf("a teacher with no grant and no copy binding A's image answered\n  %s\nwhere a missing image answers\n  %s", answer(theirs), answer(absent))
	}

	// Remove from my list is the grantee's own: anyone else, the owner
	// included, gets what a missing grant answers.
	fresh := id(a.c.must(http.StatusCreated, http.MethodPost, "/teacher/tests", map[string]any{"title": "Đề để gỡ " + nonce(t)}))
	leaving := a.grant(a.c, "test", fresh, a.mateID, "use")
	missing := mate.send(http.MethodDelete, "/teacher/shared-with-me/"+uuid.NewString(), nil)
	for who, c := range map[string]*client{"another teacher": stranger, "the owner": a.c} {
		if got := c.send(http.MethodDelete, "/teacher/shared-with-me/"+leaving, nil); answer(got) != answer(missing) {
			t.Errorf("%s removing the colleague's grant from their list answered\n  %s\nwhere a missing grant answers\n  %s", who, answer(got), answer(missing))
		}
	}
	mate.must(http.StatusNoContent, http.MethodDelete, "/teacher/shared-with-me/"+leaving, nil)
	if got := mate.send(http.MethodDelete, "/teacher/shared-with-me/"+leaving, nil); answer(got) != answer(missing) {
		t.Errorf("removing it again answered %s, want %s", answer(got), answer(missing))
	}
	if got := mate.send(http.MethodGet, "/teacher/tests/"+fresh, nil); got.status != http.StatusNotFound {
		t.Errorf("the colleague reading the test they removed from their list: %s, want 404", answer(got))
	}
	if people := a.c.must(http.StatusOK, http.MethodGet, "/teacher/shares?kind=test&resourceId="+url.QueryEscape(fresh), nil); len(people["grants"].([]any)) != 0 {
		t.Errorf("People with access still lists the colleague who left: %v", people["grants"])
	}
}
