package adapters_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"quizzivy/internal/core/adapters"
	assignmentsquery "quizzivy/internal/modules/assignments/application/query"
	assignmentsdomain "quizzivy/internal/modules/assignments/domain"
	timetabledomain "quizzivy/internal/modules/timetable/domain"
	"quizzivy/internal/shared/cqrs"
	"quizzivy/internal/shared/schedule"
)

// TestTheWeekReadsTheStudentsOwnCloseAndLimitFromTheirAssignmentList maps the
// cards of the student's assignment list to the items a week may list: the
// open and upcoming sections only, each on the student's close (an early close
// applied), with their time limit under strict and none under flexible, handed
// in once an attempt counts and live while one is under way.
func TestTheWeekReadsTheStudentsOwnCloseAndLimitFromTheirAssignmentList(t *testing.T) {
	now := time.Date(2026, 11, 23, 3, 0, 0, 0, time.UTC)
	closes, early := now.Add(48*time.Hour), now.Add(24*time.Hour)
	var asked assignmentsquery.ForStudent
	query := cqrs.HandlerFunc[assignmentsquery.ForStudent, assignmentsdomain.StudentSections](
		func(_ context.Context, q assignmentsquery.ForStudent) (assignmentsdomain.StudentSections, error) {
			asked = q
			return assignmentsdomain.StudentSections{
				DueNow: []assignmentsdomain.StudentCard{
					{ID: "open", TestTitle: "Bài mở", ClosesAt: closes, Mode: schedule.Strict, DurationMin: 45},
					{ID: "closed-early", TestTitle: "Bài đóng sớm", ClosesAt: closes, ClosedAt: &early, Mode: schedule.Flexible, DurationMin: 45},
					{ID: "retaken", TestTitle: "Bài làm lại", ClosesAt: closes, DurationMin: 20, AttemptsUsed: 1, HasLiveAttempt: true},
				},
				Upcoming:  []assignmentsdomain.StudentCard{{ID: "later", TestTitle: "Bài sắp tới", ClosesAt: closes, Mode: schedule.Strict, DurationMin: 30}},
				Completed: []assignmentsdomain.StudentCard{{ID: "done", TestTitle: "Bài đã nộp", ClosesAt: closes, AttemptsUsed: 1}},
			}, nil
		})
	got, err := adapters.TimetableDueItems{Query: query}.Due(context.Background(), "student", now)
	if err != nil {
		t.Fatal(err)
	}
	if asked.StudentID != "student" || !asked.Now.Equal(now) {
		t.Errorf("asked %+v", asked)
	}
	limit := func(n int) *int { return &n }
	want := []timetabledomain.DueAssignment{
		{ID: "open", Title: "Bài mở", ClosesAt: closes, DurationMinutes: limit(45)},
		{ID: "closed-early", Title: "Bài đóng sớm", ClosesAt: early},
		{ID: "retaken", Title: "Bài làm lại", ClosesAt: closes, HandedIn: true, Live: true, DurationMinutes: limit(20)},
		{ID: "later", Title: "Bài sắp tới", ClosesAt: closes, DurationMinutes: limit(30)},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("the items read\n  %+v\nwant\n  %+v", got, want)
	}
}
