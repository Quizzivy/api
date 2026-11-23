//go:build e2e

package e2e

import (
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"
)

// studentWeeksStayWithTheirStudents is the week's case of R2's two-student
// /app/* pair (T-R2.16, T-R8.8). getStudentWeek takes no id, so each student
// reads their own week at the same address: each teacher's student finds their
// own class's session and an assignment of theirs not started, and nothing of the
// other teacher's classes, sessions or assignments. The student who joined
// the other teacher's class reads both classes' sessions. Every object of the
// week carries exactly the keys the contract names, so no count, member,
// attendance or other student rides along.
func (x *iso) studentWeeksStayWithTheirStudents(shared *client) {
	x.t.Helper()
	sessionsOn := scheduleDay(3)
	for _, pair := range []struct {
		who        string
		own, other *party
	}{{"B's student", x.b, x.a}, {"A's student", x.a, x.b}} {
		student := pair.own.student
		due, closesOn := x.openToStudent(student)
		for _, start := range []string{sessionsOn, closesOn} {
			week := student.c.send(http.MethodGet, "/app/week?start="+start, nil)
			if week.status != http.StatusOK {
				x.t.Fatalf("%s reading the week of %s: %s", pair.who, start, answer(week))
			}
			for _, leaked := range [][]string{mentions(week.body, pair.other), mentions(week.body, pair.other.student)} {
				if len(leaked) > 0 {
					x.t.Errorf("%s's week of %s shows the other teacher's %v: %s", pair.who, start, leaked, week.body)
				}
			}
			x.weekHoldsOnlyContractKeys(pair.who, week.json)
			items := weekItems(week.json)
			switch start {
			case sessionsOn:
				if !slices.ContainsFunc(items, func(i map[string]any) bool { return i["kind"] == "session" && i["classId"] == pair.own.id("class") }) {
					x.t.Errorf("%s's week of %s holds no session of their class: %s", pair.who, start, week.body)
				}
			default:
				if !slices.ContainsFunc(items, func(i map[string]any) bool {
					return i["kind"] == "due" && i["assignmentId"] == due && i["state"] == "not_started" && i["durationMinutes"] == float64(30)
				}) {
					x.t.Errorf("%s's week of %s does not list %s, open to them and not started: %s", pair.who, start, due, week.body)
				}
			}
		}
	}

	both := shared.send(http.MethodGet, "/app/week?start="+sessionsOn, nil)
	classes := map[string]bool{}
	for _, i := range weekItems(both.json) {
		if i["kind"] == "session" {
			classes[i["classId"].(string)] = true
		}
	}
	if both.status != http.StatusOK || !classes[x.a.id("class")] || !classes[x.b.id("class")] {
		x.t.Errorf("the student of both teachers reads sessions of %v, want both classes: %s", classes, answer(both))
	}
}

// openToStudent is an assignment the student has not started and may still
// take, from their own assignment list, and the date, in Asia/Ho_Chi_Minh, it
// closes on for them.
func (x *iso) openToStudent(student *party) (string, string) {
	x.t.Helper()
	list := student.c.must(http.StatusOK, http.MethodGet, "/app/assignments", nil)
	cards, _ := list["dueNow"].([]any)
	for _, c := range cards {
		card := c.(map[string]any)
		if card["attemptsUsed"] != float64(0) || card["hasLiveAttempt"] == true {
			continue
		}
		closes, err := time.Parse(time.RFC3339, card["closesAt"].(string))
		if err != nil {
			x.t.Fatal(err)
		}
		zone, err := time.LoadLocation("Asia/Ho_Chi_Minh")
		if err != nil {
			zone = time.FixedZone("Asia/Ho_Chi_Minh", 7*60*60)
		}
		return card["id"].(string), closes.In(zone).Format(time.DateOnly)
	}
	x.t.Fatalf("the student's list holds no assignment they have not started: %v", list)
	return "", ""
}

func weekItems(week map[string]any) []map[string]any {
	var out []map[string]any
	days, _ := week["days"].([]any)
	for _, d := range days {
		items, _ := d.(map[string]any)["items"].([]any)
		for _, i := range items {
			out = append(out, i.(map[string]any))
		}
	}
	return out
}

func weekKeys(m map[string]any) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ",")
}

func (x *iso) weekHoldsOnlyContractKeys(who string, week map[string]any) {
	x.t.Helper()
	if got := weekKeys(week); got != "days,end,start" {
		x.t.Errorf("%s's week has the keys %s", who, got)
	}
	days, _ := week["days"].([]any)
	if len(days) != 7 {
		x.t.Errorf("%s's week has %d days, want 7", who, len(days))
	}
	for _, d := range days {
		if got := weekKeys(d.(map[string]any)); got != "date,items" {
			x.t.Errorf("%s's day has the keys %s", who, got)
		}
	}
	want := map[any]string{
		"session": "cancelled,classId,className,durationMinutes,kind,note,room,startsAt",
		"due":     "assignmentId,closesAt,durationMinutes,kind,state,title",
	}
	for _, i := range weekItems(week) {
		if got := weekKeys(i); got != want[i["kind"]] {
			x.t.Errorf("%s's %v item has the keys %s, want %s", who, i["kind"], got, want[i["kind"]])
		}
	}
}
