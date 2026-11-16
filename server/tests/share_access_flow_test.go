//go:build e2e

package e2e

import (
	"context"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// A shared test's operations, each with the right it needs (the contract's
// ContentAccess table), and what a reader at each level of a grant gets.
const (
	needsRead    = "read"
	needsEdit    = "edit"
	needsOwner   = "owner"
	needsContext = "context"
)

// sharedOp is one operation on A's test, its draft question, its group, its
// section or its version, built for target, the id it substitutes (A's own,
// or a missing one).
type sharedOp struct {
	name   string
	needs  string
	target string
	build  func(target string) (string, string, *payload)
}

func plainQuestion(prompt string) map[string]any {
	return map[string]any{"type": "short_answer", "prompt": prompt, "points": 1, "sampleAnswer": "Giáo viên."}
}

func plainGroupBundle() map[string]any {
	question := uuid.NewString()
	return map[string]any{
		"group": map[string]any{
			"id": uuid.NewString(), "title": "Nhóm của đề được chia sẻ",
			"members":    []any{map[string]any{"questionId": question, "optionOrder": "fixed"}},
			"stimuli":    []any{map[string]any{"id": uuid.NewString(), "title": "Bài đọc", "content": textDoc("Công viên cho thị trấn chỗ để thở."), "gaps": []any{}}},
			"recordings": []any{},
		},
		"questions": []any{map[string]any{"id": question, "input": map[string]any{
			"type": "single_choice", "prompt": "Công viên cho thị trấn điều gì?", "points": 1,
			"options": []any{map[string]any{"text": "chỗ để thở", "isCorrect": true}, map[string]any{"text": "tiếng ồn", "isCorrect": false}},
		}}},
	}
}

// TestAGrantOpensEachOfATestsOperationsAtItsLevel walks A's test, from the
// isolation world, at each level its colleague can hold to it, through the
// world's own grant: with no grant (another teacher), with Can use (the
// world's grant), with Can edit (the grant changed), and after Stop sharing.
// A reader may read, assign and see the access it holds; a Can use reader
// is refused every write with SHARE_READ_ONLY and a Can edit reader every
// owner's action with SHARE_OWNER_ONLY; and a caller who cannot read the
// test gets exactly what a missing id answers. An assignment the colleague
// made under the grant keeps its monitor, grading, review, export, analysis
// and the student's result after the grant is gone.
func TestAGrantOpensEachOfATestsOperationsAtItsLevel(t *testing.T) {
	w := bootWithStorage(t)
	a := w.teacherWorld("A")
	stranger := w.signedIn(w.createStaff("teacher"))
	mate := a.mate
	test, question, share := a.id("test"), a.id("question"), a.id("share")
	updatedAt := func() string {
		return a.c.must(http.StatusOK, http.MethodGet, "/teacher/tests/"+test, nil)["updatedAt"].(string)
	}
	classes := map[*client]string{mate: mate.class("Lớp của đồng nghiệp " + nonce(t)), stranger: stranger.class("Lớp của người lạ " + nonce(t))}

	// The colleague's class sits A's version under the Can use grant.
	learnerEmail := "hocvien-dong-nghiep-" + nonce(t) + "@example.com"
	created := mate.must(http.StatusCreated, http.MethodPost, "/teacher/students", map[string]any{
		"email": learnerEmail, "fullName": "Học viên của đồng nghiệp", "classIds": []string{classes[mate]},
	})
	assignment := id(mate.assign(a.id("test-version"), classes[mate]))
	learner := w.settledIn(learnerEmail, created["temporaryPassword"].(string))
	sitting := learner.must(http.StatusOK, http.MethodPost, "/app/assignments/"+assignment+"/attempts", nil)
	attempt := id(sitting["attempt"].(map[string]any))
	learner.must(http.StatusOK, http.MethodPost, "/app/attempts/"+attempt+"/submit", map[string]any{"sessionId": sitting["sessionId"], "reason": "manual"})
	followUps := map[string]func() sent{
		"monitor":       func() sent { return mate.send(http.MethodGet, "/teacher/assignments/"+assignment+"/attempts", nil) },
		"grading queue": func() sent { return mate.send(http.MethodGet, "/teacher/grading/queue?assignmentId="+assignment, nil) },
		"review":        func() sent { return mate.send(http.MethodGet, "/teacher/attempts/"+attempt, nil) },
		"export":        func() sent { return mate.send(http.MethodGet, "/teacher/assignments/results.csv?ids="+assignment, nil) },
		"item analysis": func() sent {
			return mate.send(http.MethodGet, "/teacher/assignments/"+assignment+"/item-analysis", nil)
		},
		"result": func() sent { return learner.send(http.MethodGet, "/app/attempts/"+attempt+"/result", nil) },
	}
	for name, read := range followUps {
		if got := read(); !success(got) {
			t.Fatalf("the colleague's %s under the grant: %s", name, answer(got))
		}
	}

	var caller *client
	ops := []sharedOp{
		{"getTest", needsRead, test, func(id string) (string, string, *payload) { return http.MethodGet, "/teacher/tests/" + id, nil }},
		{"listTestVersions", needsRead, test, func(id string) (string, string, *payload) {
			return http.MethodGet, "/teacher/tests/" + id + "/versions", nil
		}},
		{"previewTest", needsRead, test, func(id string) (string, string, *payload) {
			return http.MethodGet, "/teacher/tests/" + id + "/preview", nil
		}},
		{"getTestVersionDiff", needsRead, test, func(id string) (string, string, *payload) {
			return http.MethodGet, "/teacher/tests/" + id + "/versions/1/diff?against=draft", nil
		}},
		{"getQuestion", needsRead, question, func(id string) (string, string, *payload) { return http.MethodGet, "/teacher/questions/" + id, nil }},
		{"getQuestionGroup", needsRead, "group", func(id string) (string, string, *payload) {
			return http.MethodGet, "/teacher/question-groups/" + id, nil
		}},
		{"createAssignment", needsRead, a.id("test-version"), func(id string) (string, string, *payload) {
			body := assignmentBody(a.with("test-version", id).with("class", classes[caller]))
			body["targets"] = map[string]any{"classIds": []any{classes[caller]}, "studentIds": []any{}}
			return http.MethodPost, "/teacher/assignments", new(jsonPayload(t, body))
		}},

		// The owner's actions, before the edits move the draft.
		{"archive", needsOwner, test, func(id string) (string, string, *payload) {
			return http.MethodPatch, "/teacher/tests/" + id, new(jsonPayload(t, map[string]any{"expectedUpdatedAt": updatedAt(), "status": "archived"}))
		}},
		{"duplicateTest", needsOwner, test, func(id string) (string, string, *payload) {
			return http.MethodPost, "/teacher/tests/" + id + "/duplicate", nil
		}},
		{"deleteTest", needsOwner, test, func(id string) (string, string, *payload) { return http.MethodDelete, "/teacher/tests/" + id, nil }},
		{"deleteTestVersion", needsOwner, test, func(id string) (string, string, *payload) {
			return http.MethodDelete, "/teacher/tests/" + id + "/versions/1", nil
		}},
		{"deleteQuestion", needsOwner, question, func(id string) (string, string, *payload) {
			return http.MethodDelete, "/teacher/questions/" + id, nil
		}},
		{"duplicateQuestion", needsOwner, question, func(id string) (string, string, *payload) {
			return http.MethodPost, "/teacher/questions/" + id + "/duplicate", nil
		}},
		{"updateQuestion", needsContext, question, func(id string) (string, string, *payload) {
			return http.MethodPatch, "/teacher/questions/" + id, new(jsonPayload(t, plainQuestion("Sửa ngoài đề")))
		}},

		// The edits, each on the revision the last one left.
		{"updateTest", needsEdit, test, func(id string) (string, string, *payload) {
			return http.MethodPatch, "/teacher/tests/" + id, new(jsonPayload(t, map[string]any{"expectedUpdatedAt": updatedAt(), "title": "Đề đồng nghiệp đã sửa " + nonce(t)}))
		}},
		{"createQuestion forTestId", needsEdit, test, func(id string) (string, string, *payload) {
			return http.MethodPost, "/teacher/questions?forTestId=" + url.QueryEscape(id), new(jsonPayload(t, plainQuestion("Câu đồng nghiệp làm cho đề")))
		}},
		{"duplicateQuestion forTestId", needsEdit, question, func(id string) (string, string, *payload) {
			return http.MethodPost, "/teacher/questions/" + id + "/duplicate?forTestId=" + url.QueryEscape(test), nil
		}},
		{"createQuestionGroup in the test", needsEdit, "section", func(id string) (string, string, *payload) {
			return http.MethodPost, "/teacher/question-groups", new(jsonPayload(t, map[string]any{
				"bundle": plainGroupBundle(), "ownerSectionId": id, "expectedTestUpdatedAt": updatedAt(),
			}))
		}},
		{"deleteQuestionGroup of the test", needsEdit, "group", func(id string) (string, string, *payload) {
			return http.MethodDelete, "/teacher/question-groups/" + id + "?expectedRevision=1&expectedTestUpdatedAt=" + url.QueryEscape(updatedAt()), nil
		}},
		{"publishTest", needsEdit, test, func(id string) (string, string, *payload) {
			return http.MethodPost, "/teacher/tests/" + id + "/publish", nil
		}},
		{"setCurrentTestVersion", needsEdit, test, func(id string) (string, string, *payload) {
			return http.MethodPost, "/teacher/tests/" + id + "/versions/1/current", new(jsonPayload(t, map[string]any{"expectedUpdatedAt": updatedAt()}))
		}},
		{"createDraftFromTestVersion", needsEdit, test, func(id string) (string, string, *payload) {
			return http.MethodPost, "/teacher/tests/" + id + "/versions/1/draft", new(jsonPayload(t, map[string]any{"expectedUpdatedAt": updatedAt()}))
		}},
	}

	for _, level := range []string{"none", "use", "edit", "revoked"} {
		caller = mate
		switch level {
		case "none":
			caller = stranger
		case "edit":
			a.c.must(http.StatusOK, http.MethodPatch, "/teacher/shares/"+share, map[string]any{"access": "edit"})
		case "revoked":
			if got := a.c.send(http.MethodDelete, "/teacher/shares/"+share, nil); !success(got) {
				t.Fatalf("stopping the share: %s", answer(got))
			}
		}
		readable := level == "use" || level == "edit"
		// A restore as draft gives the draft new sections, so each level reads
		// the section and makes the test's group afresh.
		section := a.c.must(http.StatusOK, http.MethodGet, "/teacher/tests/"+test, nil)["sections"].([]any)[0].(map[string]any)["id"].(string)
		group := a.c.must(http.StatusCreated, http.MethodPost, "/teacher/question-groups", map[string]any{
			"bundle": plainGroupBundle(), "ownerSectionId": section, "expectedTestUpdatedAt": updatedAt(),
		})["bundle"].(map[string]any)["group"].(map[string]any)["id"].(string)

		read := caller.send(http.MethodGet, "/teacher/tests/"+test, nil)
		if readable {
			owner, _ := read.json["owner"].(map[string]any)
			if read.json["access"] != level || owner["id"] != a.userID || read.json["sharedWith"] != float64(0) {
				t.Errorf("the test at %s reads access %v, owner %v, sharedWith %v", level, read.json["access"], owner, read.json["sharedWith"])
			}
			q := caller.send(http.MethodGet, "/teacher/questions/"+question, nil)
			if q.json["access"] != level || q.json["owner"].(map[string]any)["id"] != a.userID {
				t.Errorf("the draft's question at %s reads access %v, owner %v", level, q.json["access"], q.json["owner"])
			}
		}
		for _, op := range ops {
			target := op.target
			switch target {
			case "group":
				target = group
			case "section":
				target = section
			}
			method, path, body := op.build(target)
			got := caller.send(method, path, body)
			where := op.name + " at " + level
			if !readable {
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
			case op.needs == needsEdit && level == "use", op.needs == needsContext && level == "use":
				want = "SHARE_READ_ONLY"
			case op.needs == needsContext:
				want = "EDIT_CONTEXT_REQUIRED"
			}
			if want == "" {
				if !success(got) {
					t.Errorf("%s: %s, want success", where, answer(got))
				}
				continue
			}
			code, _ := got.json["error"].(map[string]any)["code"].(string)
			if code != want {
				t.Errorf("%s: %s, want %s", where, answer(got), want)
			}
		}

		shared := caller.must(http.StatusOK, http.MethodGet, "/teacher/tests?include=shared&limit=100", nil)
		at := slices.IndexFunc(shared["items"].([]any), func(item any) bool { return item.(map[string]any)["id"] == test })
		if (at >= 0) != readable {
			t.Errorf("include=shared at %s lists A's test: %v", level, at >= 0)
		}
		if at >= 0 && shared["items"].([]any)[at].(map[string]any)["access"] != level {
			t.Errorf("the shared row at %s reads %v", level, shared["items"].([]any)[at])
		}
		own := caller.must(http.StatusOK, http.MethodGet, "/teacher/tests?limit=100", nil)
		if slices.ContainsFunc(own["items"].([]any), func(item any) bool { return item.(map[string]any)["id"] == test }) {
			t.Errorf("the Tests page at %s lists A's test", level)
		}
	}

	var author, published string
	if err := w.pool.QueryRow(context.Background(), `SELECT t.created_by::text, (SELECT v.published_by::text FROM app.test_versions v WHERE v.test_id = t.id ORDER BY v.version DESC LIMIT 1)
		  FROM app.tests t WHERE t.id = $1::uuid`, test).Scan(&author, &published); err != nil {
		t.Fatal(err)
	}
	if author != a.userID || published != a.mateID {
		t.Errorf("A's test is authored by %s and its newest version published by %s, want A and the colleague", author, published)
	}
	notices := a.c.must(http.StatusOK, http.MethodGet, "/me/notifications", nil)
	told := slices.ContainsFunc(notices["items"].([]any), func(item any) bool {
		n := item.(map[string]any)
		target, _ := n["target"].(map[string]any)
		return n["kind"] == "test.published_by_collaborator" && target["route"] == "test" && target["testId"] == test
	})
	if !told {
		t.Errorf("A was not told of the colleague's publish: %v", notices["items"])
	}
	for name, read := range followUps {
		got := read()
		if !success(got) {
			t.Errorf("the colleague's %s after the grant is gone: %s", name, answer(got))
		}
		if name != "export" && !strings.Contains(string(got.body), attempt) && name != "item analysis" && name != "grading queue" {
			t.Errorf("the colleague's %s after the grant is gone does not name the attempt: %s", name, got.body)
		}
	}
}
