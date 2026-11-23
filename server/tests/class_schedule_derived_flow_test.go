//go:build e2e

package e2e

import (
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"testing"
	"time"
)

func isoWeekdayOf(t *testing.T, day string) float64 {
	t.Helper()
	d, err := time.Parse("2006-01-02", day)
	if err != nil {
		t.Fatal(err)
	}
	if d.Weekday() == time.Sunday {
		return 7
	}
	return float64(d.Weekday())
}

func keysIn(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// A teacher gives a class two weekly sessions in room B2 over HTTP. The class
// reads, lists and previews with the two slots and the room derived from the
// series, its own room stays empty, and the anonymous preview carries exactly
// its five keys (T-R8.7). The student who joins sees the same weekly pattern
// on their class card, with no count.
func TestAClassScheduleComesFromItsWeeklySessionsOnEverySurface(t *testing.T) {
	w := boot(t)
	email, password := w.teacher()
	teacher := w.browser()
	teacher.login(email, password)

	classID := id(teacher.must(http.StatusCreated, http.MethodPost, "/teacher/classes", map[string]any{
		"name": "Lớp lịch học " + nonce(t), "scheduleLabel": "Lịch cũ",
	}))
	early, late := scheduleDay(1), scheduleDay(3)
	for _, day := range []string{late, early} {
		teacher.must(http.StatusCreated, http.MethodPost, "/teacher/classes/"+classID+"/sessions", map[string]any{
			"date": day, "startTime": "18:00", "durationMinutes": 90, "repeatWeekly": true, "room": "B2",
		})
	}
	first, second := isoWeekdayOf(t, early), isoWeekdayOf(t, late)
	if first > second {
		first, second = second, first
	}
	wantSlots := []any{
		map[string]any{"weekday": first, "startTime": "18:00", "durationMinutes": float64(90)},
		map[string]any{"weekday": second, "startTime": "18:00", "durationMinutes": float64(90)},
	}
	check := func(surface string, row map[string]any) {
		t.Helper()
		schedule, _ := row["schedule"].(map[string]any)
		if schedule == nil || schedule["label"] != "Lịch cũ" {
			t.Errorf("%s: the schedule is %v, want the class's label beside its slots", surface, row["schedule"])
			return
		}
		got, _ := json.Marshal(schedule["slots"])
		want, _ := json.Marshal(wantSlots)
		if string(got) != string(want) {
			t.Errorf("%s: the slots are %s, want %s", surface, got, want)
		}
		if row["room"] != "B2" {
			t.Errorf("%s: the room is %v, want B2 from the series", surface, row["room"])
		}
	}

	class := teacher.must(http.StatusOK, http.MethodGet, "/teacher/classes/"+classID, nil)
	check("getClass", class)
	if class["ownRoom"] != nil {
		t.Errorf("getClass: ownRoom is %v, want null: the class has no room of its own", class["ownRoom"])
	}
	rows := byID(items(teacher.must(http.StatusOK, http.MethodGet, "/teacher/classes?status=all&limit=100", nil)))
	check("listClasses", rows[classID])
	admin := byID(items(teacher.must(http.StatusOK, http.MethodGet, "/admin/classes?limit=50&q="+url.QueryEscape(class["name"].(string)), nil)))
	check("listAllClasses", admin[classID])

	code := teacher.must(http.StatusCreated, http.MethodPost, "/teacher/classes/"+classID+"/join-code", map[string]any{})["code"].(string)
	preview := w.browser().must(http.StatusOK, http.MethodPost, "/join/preview", map[string]any{"joinCode": code})
	if got := keysIn(preview); !slices.Equal(got, []string{"classId", "className", "room", "schedule", "teacherName"}) {
		t.Errorf("the preview carries %v, want exactly its five keys", got)
	}
	check("previewJoinCode", preview)
	if schedule, _ := preview["schedule"].(map[string]any); !slices.Equal(keysIn(schedule), []string{"label", "slots"}) {
		t.Errorf("the preview's schedule carries %v, want only label and slots", keysIn(schedule))
	}

	created := teacher.must(http.StatusCreated, http.MethodPost, "/teacher/students", map[string]any{
		"email": "student-" + nonce(t) + "@example.com", "fullName": "Nguyễn Văn An", "classIds": []string{},
	})
	student := w.browser()
	student.loginSettled(created["user"].(map[string]any)["email"].(string), created["temporaryPassword"].(string))
	joined := student.must(http.StatusOK, http.MethodPost, "/app/classes/join", map[string]any{"joinCode": code})
	check("joinClass", joined)
	mine := items(student.must(http.StatusOK, http.MethodGet, "/app/classes", nil))
	if len(mine) != 1 {
		t.Fatalf("/app/classes lists %d classes, want the one joined", len(mine))
	}
	check("listMyClasses", mine[0])
	for _, banned := range []string{"studentCount", "openAssignmentCount", "members", "averageScore", "joinCode"} {
		if _, ok := mine[0][banned]; ok {
			t.Errorf("the student's class carries %s", banned)
		}
	}
}
