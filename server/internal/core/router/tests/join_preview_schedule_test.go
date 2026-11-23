package router_test

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	classesdomain "quizzivy/internal/modules/classes/domain"
)

// The preview's two R8 fields (T-R8.7) carry values once the class has a
// weekly series: the weekly pattern and the room, the facts a teacher prints
// on a flyer, and nothing else (DG-02). TestTheSuccessBodyHasExactlyFiveKeys
// pins the key set with both null; these pin what they hold.

func previewBody(t *testing.T, result classesdomain.PreviewResult) map[string]json.RawMessage {
	t.Helper()
	rec := previewFrom(t, joinRouter(t, &fakeJoin{result: result}), "203.0.113.11", "K7M3-P9QR")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return body
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

func strPtr(s string) *string { return &s }

func scheduledPreview() classesdomain.PreviewResult {
	return classesdomain.PreviewResult{
		Outcome:     classesdomain.PreviewOK,
		ClassID:     "01935000-0000-7000-8000-0000000000c1",
		ClassName:   fakeClassName,
		TeacherName: "Thuong",
		Schedule: &classesdomain.Schedule{
			Slots: []classesdomain.ScheduleSlot{
				{Weekday: 2, StartTime: "18:00", DurationMinutes: 90},
				{Weekday: 4, StartTime: "18:00", DurationMinutes: 90},
			},
			Label: strPtr("Thứ 3, 5 · 18:00"),
		},
		Room: strPtr("Phòng 204"),
	}
}

func TestThePreviewOfAScheduledClassCarriesTheWeeklyPatternAndTheRoomAndNothingElse(t *testing.T) {
	body := previewBody(t, scheduledPreview())
	if got, want := sortedKeys(body), []string{"classId", "className", "room", "schedule", "teacherName"}; !slices.Equal(got, want) {
		t.Fatalf("the body carries %v, want exactly %v", got, want)
	}
	if string(body["room"]) != `"Phòng 204"` {
		t.Errorf("room is %s, want the derived room", body["room"])
	}

	var schedule map[string]json.RawMessage
	if err := json.Unmarshal(body["schedule"], &schedule); err != nil {
		t.Fatalf("schedule is %s: %v", body["schedule"], err)
	}
	if got := sortedKeys(schedule); !slices.Equal(got, []string{"label", "slots"}) {
		t.Fatalf("schedule carries %v, want exactly label and slots: no dates, notes, cancellations or counts", got)
	}
	if string(schedule["label"]) != `"Thứ 3, 5 · 18:00"` {
		t.Errorf("label is %s, want the class's own label", schedule["label"])
	}
	var slots []map[string]json.RawMessage
	if err := json.Unmarshal(schedule["slots"], &slots); err != nil {
		t.Fatalf("slots is %s: %v", schedule["slots"], err)
	}
	if len(slots) != 2 {
		t.Fatalf("slots is %s, want the two weekly times", schedule["slots"])
	}
	for i, s := range slots {
		if got := sortedKeys(s); !slices.Equal(got, []string{"durationMinutes", "startTime", "weekday"}) {
			t.Errorf("slot %d carries %v, want exactly weekday, startTime and durationMinutes", i, got)
		}
	}
	if string(slots[0]["weekday"]) != "2" || string(slots[0]["startTime"]) != `"18:00"` || string(slots[0]["durationMinutes"]) != "90" ||
		string(slots[1]["weekday"]) != "4" {
		t.Errorf("slots is %s, want Tuesday and Thursday at 18:00 for 90 minutes", schedule["slots"])
	}
}

func TestThePreviewOfAClassWithALabelAndNoSeriesCarriesTheLabelAndNoSlots(t *testing.T) {
	result := scheduledPreview()
	result.Schedule = &classesdomain.Schedule{Label: strPtr("Thứ 7 · 8:00")}
	result.Room = nil
	body := previewBody(t, result)
	if string(body["schedule"]) != `{"label":"Thứ 7 · 8:00","slots":[]}` {
		t.Errorf("schedule is %s, want the label and an empty list of slots", body["schedule"])
	}
	if string(body["room"]) != "null" {
		t.Errorf("room is %s, want null for a class with no room", body["room"])
	}
}

func TestARefusalCarriesNoneOfTheScheduleOrTheRoom(t *testing.T) {
	for _, outcome := range []classesdomain.PreviewOutcome{
		classesdomain.PreviewInvalid, classesdomain.PreviewRevoked, classesdomain.PreviewExpired, classesdomain.PreviewExhausted,
	} {
		result := scheduledPreview()
		result.Outcome = outcome
		rec := previewFrom(t, joinRouter(t, &fakeJoin{result: result}), "203.0.113.12", "K7M3-P9QR")
		if rec.Code != http.StatusNotFound {
			t.Fatalf("outcome %v answered %d, want 404", outcome, rec.Code)
		}
		for _, leak := range []string{"Phòng 204", "18:00", "Thứ 3", "slots", "schedule", "room"} {
			if strings.Contains(rec.Body.String(), leak) {
				t.Errorf("outcome %v leaked %q: %s", outcome, leak, rec.Body.String())
			}
		}
	}
}
