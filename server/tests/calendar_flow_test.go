//go:build e2e

package e2e

import (
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"
)

func calendarPath(from, to, classID string) string {
	path := "/teacher/calendar?from=" + from + "&to=" + to
	if classID != "" {
		path += "&classId=" + classID
	}
	return path
}

func listed(body map[string]any, key string) []map[string]any {
	raw, _ := body[key].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		out = append(out, item.(map[string]any))
	}
	return out
}

// A teacher reads two weeks of their Calendar over HTTP: a weekly session
// with one date cancelled, returned cancelled, and an assignment that opened
// before the range, drawn by its close alone and naming its class; the terms
// that end on or after the first day come with it, oldest first. A range of
// forty-three days answers 400 naming to. Another teacher sees none of it,
// and naming the first teacher's class answers 404.
func TestATeacherReadsTheirCalendar(t *testing.T) {
	r := newTermsRig(t)
	teacher := r.teacher
	className := "Lớp lịch tuần " + nonce(t)
	class := teacher.class(className)
	first := teacher.must(http.StatusCreated, http.MethodPost, "/teacher/classes/"+class+"/sessions", map[string]any{
		"date": scheduleDay(1), "startTime": "18:00", "durationMinutes": 90, "repeatWeekly": true,
	})
	next := first["seriesId"].(string) + "." + strings.ReplaceAll(scheduleDay(8), "-", "")
	teacher.must(http.StatusOK, http.MethodPatch, "/teacher/sessions/"+next, map[string]any{"cancelled": true})

	title := "Bài lịch " + nonce(t)
	_, version := teacher.publishedVersion(title)
	closes := time.Now().Add(72 * time.Hour).Truncate(time.Second)
	assignment := id(teacher.must(http.StatusCreated, http.MethodPost, "/teacher/assignments", map[string]any{
		"testVersionId":   version,
		"targets":         map[string]any{"classIds": []string{class}, "studentIds": []string{}},
		"window":          map[string]any{"opensAt": rfc3339(time.Now().Add(-48 * time.Hour)), "closesAt": rfc3339(closes)},
		"durationMinutes": 30,
		"maxAttempts":     1,
		"review":          map[string]any{"showScore": true, "showCorrectAnswers": false, "showExplanations": false},
		"integrity":       map[string]any{"requireFullscreen": false, "blockCopyPaste": true, "maxFocusLoss": 0, "onLimitExceeded": "flag", "minAwayMs": 3000},
	}))
	later := r.create("Học kỳ sau "+nonce(t), r.day(1, 2, 1), r.day(1, 6, 30))
	sooner := r.create("Học kỳ này "+nonce(t), r.day(0, 9, 1), r.day(1, 1, 15))

	calendar := teacher.must(http.StatusOK, http.MethodGet, calendarPath(scheduleDay(0), scheduleDay(14), ""), nil)
	sessions := listed(calendar, "sessions")
	if len(sessions) != 2 || sessions[0]["id"] != first["id"] || sessions[1]["id"] != next {
		t.Fatalf("the two weeks hold %v, want %v then %s", sessions, first["id"], next)
	}
	if sessions[0]["cancelled"] != false || sessions[1]["cancelled"] != true || sessions[1]["className"] != className {
		t.Errorf("the sessions read %v", sessions)
	}
	if series, _ := sessions[1]["series"].(map[string]any); series == nil || series["id"] != first["seriesId"] {
		t.Errorf("the cancelled date embeds the series %v", sessions[1]["series"])
	}
	events := listed(calendar, "assignmentEvents")
	if len(events) != 1 || events[0]["assignmentId"] != assignment || events[0]["kind"] != "closes" || events[0]["title"] != title {
		t.Fatalf("the events read %v, want the close of %s alone", events, assignment)
	}
	if at, _ := time.Parse(time.RFC3339, events[0]["at"].(string)); !at.Equal(closes) {
		t.Errorf("the close is drawn at %v, want %v", events[0]["at"], closes)
	}
	if names, _ := events[0]["classNames"].([]any); len(names) != 1 || names[0] != className {
		t.Errorf("the close names %v", events[0]["classNames"])
	}
	var terms []string
	for _, term := range listed(calendar, "terms") {
		terms = append(terms, term["id"].(string))
	}
	at := func(id string) int { return slices.Index(terms, id) }
	if at(id(sooner)) < 0 || at(id(later)) < at(id(sooner)) {
		t.Errorf("the terms read %v, want %s before %s", terms, id(sooner), id(later))
	}

	refused := teacher.must(http.StatusBadRequest, http.MethodGet, calendarPath(scheduleDay(0), scheduleDay(43), ""), nil)
	if code, details := sessionError(refused); code != "VALIDATION_FAILED" || details["to"] == nil {
		t.Errorf("forty-three days answered %s %v", code, details)
	}
	teacher.must(http.StatusOK, http.MethodGet, calendarPath(scheduleDay(0), scheduleDay(42), class), nil)

	other := r.w.signedIn(r.w.createStaff("teacher"))
	theirs := other.must(http.StatusOK, http.MethodGet, calendarPath(scheduleDay(0), scheduleDay(14), ""), nil)
	if len(listed(theirs, "sessions")) != 0 || len(listed(theirs, "assignmentEvents")) != 0 {
		t.Errorf("another teacher's calendar reads %v", theirs)
	}
	other.must(http.StatusNotFound, http.MethodGet, calendarPath(scheduleDay(0), scheduleDay(14), class), nil)
}
