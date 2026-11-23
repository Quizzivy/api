//go:build e2e

package e2e

import (
	"net/http"
	"testing"
)

func sessionError(body map[string]any) (code string, details map[string]any) {
	e, _ := body["error"].(map[string]any)
	code, _ = e["code"].(string)
	details, _ = e["details"].(map[string]any)
	return code, details
}

// A teacher keeps a class's timetable over HTTP: a weekly session meeting in
// the class's room, one date of it given its own room and back, a removal
// refused for an occurrence and allowed for a one-off, a change from a date
// on, and an end before its first date that removes it.
func TestATeacherKeepsAClassTimetable(t *testing.T) {
	w := boot(t)
	teacher := w.signedIn(w.createStaff("teacher"))
	class := id(teacher.must(http.StatusCreated, http.MethodPost, "/teacher/classes", map[string]any{"name": "Lớp lịch " + nonce(t), "room": "P.204"}))
	sessions := "/teacher/classes/" + class + "/sessions"

	first := teacher.must(http.StatusCreated, http.MethodPost, sessions, map[string]any{
		"date": scheduleDay(7), "startTime": "18:00", "durationMinutes": 90, "repeatWeekly": true,
	})
	series, _ := first["series"].(map[string]any)
	if series == nil || first["seriesId"] != series["id"] || first["room"] != "P.204" || series["room"] != "P.204" || series["timeZone"] != "Asia/Ho_Chi_Minh" {
		t.Fatalf("the first occurrence reads %v", first)
	}
	if want := series["id"].(string) + "." + scheduleDay(7)[0:4] + scheduleDay(7)[5:7] + scheduleDay(7)[8:10]; first["id"] != want || first["date"] != scheduleDay(7) {
		t.Errorf("the first occurrence is %v on %v, want %s", first["id"], first["date"], want)
	}

	occurrence := "/teacher/sessions/" + first["id"].(string)
	own := teacher.must(http.StatusOK, http.MethodPatch, occurrence, map[string]any{"room": "  Phòng 9 ", "note": "Mang từ điển"})
	if own["room"] != "Phòng 9" || own["note"] != "Mang từ điển" || own["changed"] != true || own["id"] != first["id"] {
		t.Errorf("the date with its own room reads %v", own)
	}
	back := teacher.must(http.StatusOK, http.MethodPatch, occurrence, map[string]any{"room": nil, "note": nil})
	if back["room"] != "P.204" || back["note"] != nil || back["changed"] != false {
		t.Errorf("the date back to its series reads %v", back)
	}

	if code, _ := sessionError(teacher.must(http.StatusConflict, http.MethodDelete, occurrence, nil)); code != "SESSION_IN_SERIES" {
		t.Errorf("removing an occurrence answered %s", code)
	}
	teacher.must(http.StatusNotFound, http.MethodPatch, "/teacher/sessions/not-a-session", map[string]any{"cancelled": true})
	teacher.must(http.StatusNotFound, http.MethodDelete, "/teacher/sessions/"+series["id"].(string)+".20260231", nil)

	oneOff := teacher.must(http.StatusCreated, http.MethodPost, sessions, map[string]any{
		"date": scheduleDay(3), "startTime": "08:30", "durationMinutes": 60, "repeatWeekly": false, "room": nil,
	})
	if oneOff["seriesId"] != nil || oneOff["series"] != nil || oneOff["room"] != nil || oneOff["date"] != scheduleDay(3) {
		t.Errorf("the one-off reads %v", oneOff)
	}
	teacher.must(http.StatusNoContent, http.MethodDelete, "/teacher/sessions/"+id(oneOff), nil)
	teacher.must(http.StatusNotFound, http.MethodDelete, "/teacher/sessions/"+id(oneOff), nil)

	for field, body := range map[string]map[string]any{
		"endsOn": {"date": scheduleDay(3), "startTime": "08:30", "durationMinutes": 60, "repeatWeekly": false, "endsOn": scheduleDay(30)},
		"date":   {"date": scheduleDay(800), "startTime": "08:30", "durationMinutes": 60, "repeatWeekly": false},
	} {
		code, details := sessionError(teacher.must(http.StatusBadRequest, http.MethodPost, sessions, body))
		if _, named := details[field]; code != "VALIDATION_FAILED" || !named {
			t.Errorf("a bad %s answered %s %v", field, code, details)
		}
	}

	seriesPath := "/teacher/session-series/" + series["id"].(string)
	code, details := sessionError(teacher.must(http.StatusBadRequest, http.MethodPatch, seriesPath, map[string]any{"fromDate": scheduleDay(-1), "durationMinutes": 120}))
	if _, named := details["fromDate"]; code != "VALIDATION_FAILED" || !named {
		t.Errorf("a change from yesterday answered %s %v", code, details)
	}
	edited := teacher.must(http.StatusOK, http.MethodPatch, seriesPath, map[string]any{"fromDate": scheduleDay(14), "durationMinutes": 120})
	next, _ := edited["series"].(map[string]any)
	if next == nil || next["id"] == series["id"] || next["startsOn"] != scheduleDay(14) || next["durationMinutes"] != float64(120) || edited["droppedChanges"] != float64(0) {
		t.Fatalf("the change from a date on answered %v", edited)
	}
	ended := teacher.must(http.StatusOK, http.MethodPost, seriesPath+"/end", map[string]any{"lastDate": scheduleDay(7)})
	if ended["endsOn"] != scheduleDay(7) {
		t.Errorf("ending the old series answered %v", ended)
	}
	nextPath := "/teacher/session-series/" + next["id"].(string)
	teacher.must(http.StatusNoContent, http.MethodPost, nextPath+"/end", map[string]any{"lastDate": scheduleDay(13)})
	teacher.must(http.StatusNotFound, http.MethodPost, nextPath+"/end", map[string]any{"lastDate": scheduleDay(13)})

	teacher.must(http.StatusOK, http.MethodPatch, "/teacher/classes/"+class, map[string]any{"archived": true})
	if code, _ := sessionError(teacher.must(http.StatusConflict, http.MethodPost, sessions, map[string]any{
		"date": scheduleDay(3), "startTime": "08:30", "durationMinutes": 60, "repeatWeekly": false,
	})); code != "CLASS_ARCHIVED" {
		t.Errorf("a session for an archived class answered %s", code)
	}
}
