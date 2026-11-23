//go:build integration

package repositories_test

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"quizzivy/internal/platform/email"
)

// session stores a one-off of class starting at startsAt, cancelled when
// asked, and marks each student with the status given.
func (w *dueWorld) session(class, teacher string, startsAt time.Time, cancelled bool, marks map[string]string) {
	w.t.Helper()
	var cancelledAt *time.Time
	if cancelled {
		cancelledAt = &startsAt
	}
	id := w.id(`INSERT INTO app.class_sessions (class_id, starts_at, duration_minutes, cancelled_at, created_by)
		VALUES ($1, $2, 90, $3, $4) RETURNING id::text`, class, startsAt, cancelledAt, teacher)
	for student, status := range marks {
		w.exec(`INSERT INTO app.attendance_marks (session_id, student_id, status, marked_by, marked_at) VALUES ($1, $2, $3, $4, $5)`,
			id, student, status, teacher, startsAt)
	}
}

func (w *dueWorld) weeklyParams(teacher string) email.WeeklySummaryParams {
	w.t.Helper()
	queue := w.outbox(teacher)
	if len(queue) != 1 {
		w.t.Fatalf("the teacher's queue %+v, want the summary alone", queue)
	}
	var params email.WeeklySummaryParams
	if err := json.Unmarshal([]byte(queue[0].params), &params); err != nil {
		w.t.Fatal(err)
	}
	return params
}

// TestTheWeeklySummaryCarriesEachClasssAttendanceFromLastWeeksMarkedSessions
// writes the summary of a teacher in Asia/Ho_Chi_Minh for the week of Monday
// 4 March: one line per active class that had a session marked that week,
// its rate over the marks of those sessions, late counted as attended, and
// how many there were. A cancelled session, an unmarked one, the sessions
// either side of the week and an archived class's count for nothing, and a
// class with no session marked has no line.
func TestTheWeeklySummaryCarriesEachClasssAttendanceFromLastWeeksMarkedSessions(t *testing.T) {
	w := newFarWorld(t)
	hcm, err := time.LoadLocation("Asia/Ho_Chi_Minh")
	if err != nil {
		t.Fatal(err)
	}
	at := func(clock string) time.Time {
		v, err := time.ParseInLocation("2006-01-02 15:04", clock, hcm)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	teacher := w.user("teacher")
	ielts := w.id(`INSERT INTO app.classes (name, teacher_id) VALUES ('IELTS 7.0', $1) RETURNING id::text`, teacher)
	toeic := w.id(`INSERT INTO app.classes (name, teacher_id) VALUES ('TOEIC 2', $1) RETURNING id::text`, teacher)
	quiet := w.id(`INSERT INTO app.classes (name, teacher_id) VALUES ('Giao tiếp', $1) RETURNING id::text`, teacher)
	archived := w.id(`INSERT INTO app.classes (name, teacher_id, archived_at) VALUES ('Lớp cũ', $1, $2) RETURNING id::text`, teacher, at("2041-03-10 12:00"))
	s := []string{w.user("student"), w.user("student"), w.user("student")}
	for _, student := range s {
		w.join(ielts, student)
		w.join(toeic, student)
	}
	a := w.assignment(spec{creator: teacher, classes: []string{ielts}, opens: at("2041-02-20 08:00"), closes: at("2041-03-20 08:00")})
	w.attempt(a, s[0], "graded", at("2041-03-06 20:00"))

	w.session(ielts, teacher, at("2041-03-04 00:00"), false, map[string]string{s[0]: "present", s[1]: "late", s[2]: "absent"})
	w.session(ielts, teacher, at("2041-03-06 18:00"), false, map[string]string{s[0]: "present", s[1]: "present", s[2]: "absent"})
	w.session(ielts, teacher, at("2041-03-08 18:00"), true, map[string]string{s[0]: "absent", s[1]: "absent", s[2]: "absent"})
	w.session(ielts, teacher, at("2041-03-09 18:00"), false, nil)
	w.session(ielts, teacher, at("2041-03-03 23:59"), false, map[string]string{s[0]: "absent"})
	w.session(ielts, teacher, at("2041-03-11 00:00"), false, map[string]string{s[0]: "absent"})
	w.session(toeic, teacher, at("2041-03-10 23:59"), false, map[string]string{s[0]: "present", s[1]: "present"})
	w.session(quiet, teacher, at("2041-03-07 18:00"), false, nil)
	w.session(archived, teacher, at("2041-03-05 18:00"), false, map[string]string{s[0]: "absent"})

	w.runWeekly(w.scheduler(nil), at("2041-03-11 07:00"))
	params := w.weeklyParams(teacher)
	want := []email.WeeklyAttendance{{ClassName: "IELTS 7.0", Rate: 67, Sessions: 2}, {ClassName: "TOEIC 2", Rate: 100, Sessions: 1}}
	if !params.WeekOf.Equal(time.Date(2041, 3, 4, 0, 0, 0, 0, time.UTC)) || !reflect.DeepEqual(params.Attendance, want) {
		t.Errorf("the summary of the week of %s carries %+v, want %+v", params.WeekOf, params.Attendance, want)
	}
}

// TestAWeekWithNoSessionMarkedCarriesNoAttendance writes the summary of a
// teacher whose class held a session that week that nobody marked: the
// summary goes out with its paper counts and no attendance at all.
func TestAWeekWithNoSessionMarkedCarriesNoAttendance(t *testing.T) {
	w := newFarWorld(t)
	teacher := w.user("teacher")
	class := w.id(`INSERT INTO app.classes (name, teacher_id) VALUES ('IELTS 7.0', $1) RETURNING id::text`, teacher)
	student := w.user("student")
	w.join(class, student)
	seven := time.Date(2041, 3, 11, 0, 0, 0, 0, time.UTC)
	a := w.assignment(spec{creator: teacher, classes: []string{class}, opens: seven.Add(-10 * 24 * time.Hour), closes: seven.Add(10 * 24 * time.Hour)})
	w.attempt(a, student, "graded", seven.Add(-48*time.Hour))
	w.session(class, teacher, seven.Add(-72*time.Hour), false, nil)

	w.runWeekly(w.scheduler(nil), seven)
	if params := w.weeklyParams(teacher); params.Count != 1 || params.Attendance != nil {
		t.Errorf("the summary says %+v, want one paper and no attendance", params)
	}
	var raw string
	if err := w.tx.QueryRow(t.Context(), `SELECT params::text FROM app.email_outbox WHERE to_user_id = $1::uuid`, teacher).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var stored map[string]any
	if err := json.Unmarshal([]byte(raw), &stored); err != nil {
		t.Fatal(err)
	}
	if _, has := stored["attendance"]; has {
		t.Errorf("the stored params name attendance: %s", raw)
	}
}
