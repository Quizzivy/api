//go:build e2e

package e2e

import (
	"net/http"
	"testing"
	"time"
)

// F1 over HTTP: an assignment is strict unless it names flexible, its mode
// changes before the open and locks while open, and the teacher's default
// mode is stored and returned.
func TestTheAssignmentModeIsWrittenLockedAndDefaulted(t *testing.T) {
	w := boot(t)
	email, password := w.teacher()
	teacher := w.browser()
	teacher.login(email, password)
	_, versionID := teacher.publishedVersion("F1 modes " + nonce(t))
	classID := teacher.class("Lớp F1 " + nonce(t))

	open := teacher.assign(versionID, classID)
	if open["mode"] != "strict" {
		t.Fatalf("an assignment that names no mode reads %v, want strict", open["mode"])
	}
	body := func(opens time.Time, mode string) map[string]any {
		return map[string]any{
			"testVersionId":   versionID,
			"targets":         map[string]any{"classIds": []string{classID}, "studentIds": []string{}},
			"window":          map[string]any{"opensAt": rfc3339(opens), "closesAt": rfc3339(opens.Add(48 * time.Hour))},
			"durationMinutes": 30,
			"maxAttempts":     1,
			"mode":            mode,
			"review":          map[string]any{"showScore": true, "showCorrectAnswers": false, "showExplanations": false},
			"integrity":       map[string]any{"requireFullscreen": false, "blockCopyPaste": true, "maxFocusLoss": 0, "onLimitExceeded": "flag", "minAwayMs": 3000},
		}
	}
	opened := time.Now().Add(-time.Minute)
	status, refused := teacher.call(http.MethodPatch, "/teacher/assignments/"+id(open), body(opened, "flexible"))
	if problem, _ := refused["error"].(map[string]any); status != http.StatusConflict || problem["code"] != "ASSIGNMENT_LOCKED" {
		t.Errorf("a mode change while open answered %d %v, want 409 ASSIGNMENT_LOCKED", status, refused)
	}

	later := time.Now().Add(time.Hour)
	scheduled := teacher.must(http.StatusCreated, http.MethodPost, "/teacher/assignments", body(later, "flexible"))
	if scheduled["mode"] != "flexible" {
		t.Fatalf("a flexible create reads %v", scheduled["mode"])
	}
	if changed := teacher.must(http.StatusOK, http.MethodPatch, "/teacher/assignments/"+id(scheduled), body(later, "strict")); changed["mode"] != "strict" {
		t.Errorf("a change before the open reads %v, want strict", changed["mode"])
	}
	teacher.must(http.StatusBadRequest, http.MethodPost, "/teacher/assignments", body(later, "relaxed"))

	prefs := teacher.must(http.StatusOK, http.MethodPatch, "/me/preferences", map[string]any{"assignmentDefaults": map[string]any{"mode": "flexible", "durationMinutes": 45}})
	if defaults, ok := prefs["assignmentDefaults"].(map[string]any); !ok || defaults["mode"] != "flexible" {
		t.Errorf("the stored default reads %v", prefs["assignmentDefaults"])
	}
	teacher.must(http.StatusBadRequest, http.MethodPatch, "/me/preferences", map[string]any{"assignmentDefaults": map[string]any{"mode": "relaxed"}})
}
