//go:build integration

package application_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"quizzivy/internal/modules/attempts/domain"
	"quizzivy/internal/shared/access"
)

func flaggedIDs(t *testing.T, w *paperWorld, scope access.Scope, student string, since time.Time) []string {
	t.Helper()
	found, err := w.timelines.Flagged(context.Background(), scope, student, since)
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, len(found))
	for i, f := range found {
		ids[i] = f.AttemptID
	}
	return ids
}

// The context events of a staff member's direct thread (T-R7.10b): a
// student's flagged attempts on the assignments the reader's scope reaches,
// at coalesce(submitted_at, started_at), from a moment on, oldest first.
func TestFlaggedAttemptsFollowTheAssignmentScopeAndTheirMoment(t *testing.T) {
	w := newPaperWorld(t)
	ctx := context.Background()
	if _, err := w.tx.Exec(ctx, `UPDATE app.attempts SET flagged = true, focus_loss_count = 3 WHERE id = ANY($1::uuid[])`,
		[]string{w.paperA, w.live, w.paperB}); err != nil {
		t.Fatal(err)
	}
	var submitted, started time.Time
	if err := w.tx.QueryRow(ctx, `SELECT (SELECT submitted_at FROM app.attempts WHERE id = $1), (SELECT started_at FROM app.attempts WHERE id = $2)`,
		w.paperA, w.live).Scan(&submitted, &started); err != nil {
		t.Fatal(err)
	}
	a, b, all := access.Scope{UserID: w.a}, access.Scope{UserID: w.b}, access.Scope{UserID: w.admin, All: true}
	hourAgo := time.Now().Add(-time.Hour)

	found, err := w.timelines.Flagged(ctx, a, w.studentA, hourAgo)
	if err != nil {
		t.Fatal(err)
	}
	want := []domain.FlaggedAttempt{
		{AttemptID: w.live, AssignmentID: w.ofA, Title: "Đề", FocusLost: 3, At: started},
		{AttemptID: w.paperA, AssignmentID: w.ofA, Title: "Đề", FocusLost: 3, At: submitted},
	}
	if len(found) != len(want) {
		t.Fatalf("A's view of the student's flagged attempts: %+v, want the live attempt at its start, then the handed-in one at its hand-in", found)
	}
	for i := range want {
		if found[i].AttemptID != want[i].AttemptID || found[i].AssignmentID != want[i].AssignmentID || found[i].Title != want[i].Title ||
			found[i].FocusLost != want[i].FocusLost || !found[i].At.Equal(want[i].At) {
			t.Errorf("flagged attempt %d: %+v, want %+v", i, found[i], want[i])
		}
	}
	for label, c := range map[string]struct {
		scope   access.Scope
		student string
		since   time.Time
		want    []string
	}{
		"another teacher's scope":                  {b, w.studentA, hourAgo, nil},
		"scope.all":                                {all, w.studentA, hourAgo, []string{w.live, w.paperA}},
		"a window opening after the live start":    {a, w.studentA, started.Add(time.Second), []string{w.paperA}},
		"a window opening after both":              {a, w.studentA, submitted.Add(time.Second), nil},
		"an admin's assignment on B's class, by B": {b, w.studentB, hourAgo, []string{w.paperB}},
		"the same paper, for A":                    {a, w.studentB, hourAgo, nil},
		"a student with no flagged attempt":        {all, w.loose, hourAgo, nil},
	} {
		if got := flaggedIDs(t, w, c.scope, c.student, c.since); !slices.Equal(got, c.want) {
			t.Errorf("%s: %v, want %v", label, got, c.want)
		}
	}
}
