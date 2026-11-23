//go:build e2e

package e2e

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func registerKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// A teacher takes a register over HTTP: yesterday's one-off reads its roster
// with no mark and an overall rate, a save with a note answers the register
// as saved, a save naming a stranger, a note beside a null status, a student
// twice or a note over 500 characters is refused naming the ids, a session
// next week and a cancelled one are refused with their codes, and the list
// shows the marked session with its counts beside the badge of the others.
func TestATeacherTakesARegister(t *testing.T) {
	w := boot(t)
	teacher := w.signedIn(w.createStaff("teacher"))
	class := id(teacher.must(http.StatusCreated, http.MethodPost, "/teacher/classes", map[string]any{"name": "Lớp điểm danh " + nonce(t), "room": "P.3"}))
	created := teacher.must(http.StatusCreated, http.MethodPost, "/teacher/students", map[string]any{
		"email": "diemdanh-" + nonce(t) + "@example.com", "fullName": "Học viên điểm danh", "classIds": []string{class},
	})
	student := created["user"].(map[string]any)["id"].(string)
	sessions := "/teacher/classes/" + class + "/sessions"
	yesterday := id(teacher.must(http.StatusCreated, http.MethodPost, sessions, map[string]any{
		"date": scheduleDay(-1), "startTime": "08:00", "durationMinutes": 60, "repeatWeekly": false,
	}))
	weekly := teacher.must(http.StatusCreated, http.MethodPost, sessions, map[string]any{
		"date": scheduleDay(-2), "startTime": "08:00", "durationMinutes": 60, "repeatWeekly": true,
	})
	register := "/teacher/sessions/" + yesterday + "/attendance"

	read := teacher.must(http.StatusOK, http.MethodGet, register, nil)
	if want := []string{"counts", "rateScope", "session", "students", "term"}; !slices.Equal(registerKeys(read), want) || read["rateScope"] != "overall" || read["term"] != nil {
		t.Fatalf("the register reads %v", read)
	}
	students, _ := read["students"].([]any)
	if len(students) != 1 {
		t.Fatalf("the roster is %v", read["students"])
	}
	row := students[0].(map[string]any)
	if want := []string{"avatarUrl", "fullName", "note", "rate", "status", "studentId"}; !slices.Equal(registerKeys(row), want) || row["studentId"] != student || row["status"] != nil || row["rate"] != nil {
		t.Errorf("an unmarked student reads %v", row)
	}

	saved := teacher.must(http.StatusOK, http.MethodPut, register, map[string]any{"marks": []any{map[string]any{"studentId": student, "status": "late", "note": "  Kẹt xe "}}})
	row = saved["students"].([]any)[0].(map[string]any)
	counts := saved["counts"].(map[string]any)
	if row["status"] != "late" || row["note"] != "Kẹt xe" || row["rate"] != float64(100) || counts["late"] != float64(1) || counts["unmarked"] != float64(0) {
		t.Errorf("the saved register reads %v, counts %v", row, counts)
	}
	if session := saved["session"].(map[string]any); session["hasAttendance"] != true || session["id"] != yesterday {
		t.Errorf("the saved register's session reads %v", session)
	}
	kept := teacher.must(http.StatusOK, http.MethodPut, register, map[string]any{"marks": []any{map[string]any{"studentId": student, "status": "absent"}}})
	if row = kept["students"].([]any)[0].(map[string]any); row["note"] != "Kẹt xe" || row["rate"] != float64(0) {
		t.Errorf("a save that leaves the note out reads %v", row)
	}

	stranger := uuid.NewString()
	for name, c := range map[string]struct {
		marks []any
		named []string
	}{
		"a stranger":              {[]any{map[string]any{"studentId": student, "status": "present"}, map[string]any{"studentId": stranger, "status": "present"}}, []string{stranger}},
		"a note beside no status": {[]any{map[string]any{"studentId": student, "status": nil, "note": "Ốm"}}, []string{student}},
		"a student named twice":   {[]any{map[string]any{"studentId": student, "status": "present"}, map[string]any{"studentId": student, "status": nil}}, []string{student}},
	} {
		code, details := sessionError(teacher.must(http.StatusUnprocessableEntity, http.MethodPut, register, map[string]any{"marks": c.marks}))
		named, _ := details["studentIds"].(string)
		if code != "VALIDATION_FAILED" || !strings.Contains(named, c.named[0]) || (name == "a stranger" && strings.Contains(named, student)) {
			t.Errorf("%s answered %s %v", name, code, details)
		}
	}
	// The contract's maxLength refuses a note over 500 characters before the
	// handler; the handler refuses one that NFC makes longer (domain tests).
	if code, _ := sessionError(teacher.must(http.StatusBadRequest, http.MethodPut, register, map[string]any{"marks": []any{map[string]any{"studentId": student, "status": "present", "note": strings.Repeat("a", 501)}}})); code != "VALIDATION_FAILED" {
		t.Errorf("a note of 501 characters answered %s", code)
	}
	if row = teacher.must(http.StatusOK, http.MethodGet, register, nil)["students"].([]any)[0].(map[string]any); row["status"] != "absent" || row["note"] != "Kẹt xe" {
		t.Errorf("after the refusals the student reads %v", row)
	}

	next := "/teacher/sessions/" + weekly["seriesId"].(string) + "." + strings.ReplaceAll(scheduleDay(5), "-", "") + "/attendance"
	if code, _ := sessionError(teacher.must(http.StatusUnprocessableEntity, http.MethodPut, next, map[string]any{"marks": []any{map[string]any{"studentId": student, "status": "present"}}})); code != "SESSION_NOT_STARTED" {
		t.Errorf("a session next week answered %s", code)
	}
	teacher.must(http.StatusOK, http.MethodPatch, "/teacher/sessions/"+id(weekly), map[string]any{"cancelled": true})
	if code, _ := sessionError(teacher.must(http.StatusConflict, http.MethodPut, "/teacher/sessions/"+id(weekly)+"/attendance", map[string]any{"marks": []any{map[string]any{"studentId": student, "status": "present"}}})); code != "SESSION_CANCELLED" {
		t.Errorf("a cancelled session answered %s", code)
	}
	teacher.must(http.StatusNotFound, http.MethodGet, "/teacher/sessions/not-a-session/attendance", nil)

	list := teacher.must(http.StatusOK, http.MethodGet, "/teacher/attendance/sessions?classId="+class+"&limit=1", nil)
	items, _ := list["items"].([]any)
	if len(items) != 1 || list["notMarked"] != float64(0) || list["nextBefore"] != nil {
		t.Fatalf("the list reads %v", list)
	}
	item := items[0].(map[string]any)
	if want := []string{"classId", "className", "counts", "id", "marked", "room", "startsAt", "studentCount"}; !slices.Equal(registerKeys(item), want) ||
		item["id"] != yesterday || item["marked"] != true || item["studentCount"] != float64(1) || item["room"] != "P.3" {
		t.Errorf("the marked session lists %v", item)
	}
	if c := item["counts"].(map[string]any); c["absent"] != float64(1) || c["present"] != float64(0) {
		t.Errorf("its counts are %v", c)
	}
	teacher.must(http.StatusNotFound, http.MethodGet, "/teacher/attendance/sessions?classId="+uuid.NewString(), nil)
}
