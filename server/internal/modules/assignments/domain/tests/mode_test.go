package domain_test

import (
	"errors"
	"testing"
	"time"

	"quizzivy/internal/modules/assignments/domain"
	"quizzivy/internal/shared/schedule"
)

// F1: the mode locks with the time limit while the assignment is open, and
// also once any attempt exists; before the open, with no attempt, it changes.
func TestTheModeLocksWhileOpenOrOnceAnAttemptExists(t *testing.T) {
	for _, c := range []struct {
		name    string
		next    domain.Mode
		status  domain.Status
		started bool
		locked  bool
	}{
		{"a change before the open", schedule.Flexible, domain.Scheduled, false, false},
		{"a change to a draft", schedule.Flexible, domain.Draft, false, false},
		{"a change while open", schedule.Flexible, domain.Open, false, true},
		{"a change before the open once an attempt exists", schedule.Flexible, domain.Scheduled, true, true},
		{"a change after the close once an attempt exists", schedule.Flexible, domain.Closed, true, true},
		{"the stored mode sent while open", schedule.Strict, domain.Open, true, false},
		{"no mode named while open", "", domain.Open, true, false},
	} {
		if got := domain.ModeLocked(schedule.Strict, c.next, c.status, c.started); got != c.locked {
			t.Errorf("%s: locked %v, want %v", c.name, got, c.locked)
		}
	}
	if !errors.Is(domain.ErrModeLocked, domain.ErrAssignmentLocked) {
		t.Error("ErrModeLocked is not an ErrAssignmentLocked, so it would not answer ASSIGNMENT_LOCKED")
	}
}

func TestAnUnknownModeIsRefusedOnItsField(t *testing.T) {
	now := time.Date(2026, 11, 18, 9, 0, 0, 0, time.UTC)
	in := domain.WriteInput{
		ClassIDs: []string{"class"}, OpensAt: now, ClosesAt: now.Add(time.Hour),
		Review: domain.Review{Release: domain.ReleaseOnSubmit}, Now: now, Mode: "relaxed",
	}
	var invalid *domain.ValidationError
	if err := in.Validate(); !errors.As(err, &invalid) || len(invalid.Fields) != 1 || invalid.Fields[0].Field != "mode" {
		t.Fatalf("an unknown mode: %v", err)
	}
	in.Mode = schedule.Flexible
	if err := in.Validate(); err != nil {
		t.Errorf("flexible was refused: %v", err)
	}
}
