//go:build integration

package repositories_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"quizzivy/internal/modules/assignments/domain"
	"quizzivy/internal/shared/access"
	"quizzivy/internal/shared/schedule"
)

func (w *reachWorld) modeOf(t *testing.T, id string) domain.Mode {
	t.Helper()
	a, err := w.store.Get(context.Background(), access.Scope{All: true}, id)
	if err != nil {
		t.Fatal(err)
	}
	return a.Mode
}

// update saves in over the assignment id as its owner w.a, keeping the
// stored window unless in names another.
func (w *reachWorld) update(id string, in domain.WriteInput) error {
	req := as(w.a, false)
	req.ID = id
	_, err := w.store.Update(context.Background(), req, in)
	return err
}

// F1: a create without a mode is strict; the mode is stored and read back,
// an update that names none keeps it, and a duplicate copies it.
func TestTheModeIsWrittenReadKeptAndCopied(t *testing.T) {
	w := newReachWorld(t)
	if got := w.modeOf(t, w.mineA); got != schedule.Strict {
		t.Errorf("a create without a mode reads %q, want strict", got)
	}
	in := input(w.versionA, []string{w.classA}, nil)
	in.OpensAt, in.ClosesAt, in.Mode = time.Now().Add(time.Hour), time.Now().Add(48*time.Hour), schedule.Flexible
	flexible := w.create(t, as(w.a, false), in)
	if got := w.modeOf(t, flexible); got != schedule.Flexible {
		t.Fatalf("a flexible create reads %q", got)
	}
	in.Mode, in.DurationMin = "", 50
	if err := w.update(flexible, in); err != nil {
		t.Fatal(err)
	}
	if got := w.modeOf(t, flexible); got != schedule.Flexible {
		t.Errorf("an update naming no mode left %q, want the stored flexible", got)
	}
	req := as(w.a, false)
	req.ID = flexible
	copied, err := w.store.Duplicate(context.Background(), req, []string{w.classA}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if copied.Mode != schedule.Flexible {
		t.Errorf("the duplicate reads %q, want the original's flexible", copied.Mode)
	}
}

// The mode locks while the assignment is open, and once any attempt exists
// even before the open; before the open and with no attempt it changes.
func TestTheModeLocksWhileOpenOrOnceAnAttemptExists(t *testing.T) {
	w := newReachWorld(t)
	later := input(w.versionA, []string{w.classA}, nil)
	later.OpensAt, later.ClosesAt = time.Now().Add(time.Hour), time.Now().Add(5*time.Hour)
	scheduled := w.create(t, as(w.a, false), later)

	later.Mode = schedule.Flexible
	if err := w.update(scheduled, later); err != nil {
		t.Fatalf("a change before the open was refused: %v", err)
	}
	w.sitting(t, scheduled, w.studentA, time.Minute, time.Now().Add(time.Hour), "voided")
	later.Mode = schedule.Strict
	if err := w.update(scheduled, later); !errors.Is(err, domain.ErrModeLocked) {
		t.Errorf("a change before the open with an attempt: %v, want ErrModeLocked", err)
	}

	open := input(w.versionA, []string{w.classA}, nil)
	open.Mode = schedule.Flexible
	if err := w.update(w.mineA, open); !errors.Is(err, domain.ErrModeLocked) || !errors.Is(err, domain.ErrAssignmentLocked) {
		t.Errorf("a change while open: %v, want ErrModeLocked, an ErrAssignmentLocked", err)
	}
	open.Mode = schedule.Strict
	if err := w.update(w.mineA, open); err != nil {
		t.Errorf("sending the stored mode while open was refused: %v", err)
	}
	if got := w.modeOf(t, w.mineA); got != schedule.Strict {
		t.Errorf("the open assignment reads %q after the refusal, want strict", got)
	}
}

// An extension, and an override, move a flexible attempt to its student's
// new close; the override's time limit is not applied.
func TestAFlexibleAttemptMovesWithItsStudentsClose(t *testing.T) {
	w := newReachWorld(t)
	ctx := context.Background()
	in := input(w.versionA, []string{w.classA}, nil)
	in.ClosesAt, in.DurationMin, in.Mode = time.Now().Add(20*time.Minute), 45, schedule.Flexible
	assignment := w.create(t, as(w.a, false), in)
	extended, overridden := w.classmate(t, w.classA, w.a), w.classmate(t, w.classA, w.a)
	closes := w.closesAt(t, assignment)
	extendedAttempt := w.sitting(t, assignment, extended, 5*time.Minute, closes, "in_progress")
	overriddenAttempt := w.sitting(t, assignment, overridden, 5*time.Minute, closes, "in_progress")

	req := as(w.a, false)
	req.ID = assignment
	if _, err := w.store.Extend(ctx, req, 120, false, time.Now()); err != nil {
		t.Fatal(err)
	}
	moved := w.closesAt(t, assignment)
	if got := w.deadlineOf(t, extendedAttempt); !got.Equal(moved) {
		t.Errorf("the flexible attempt has deadline %v after the extension, want the new close %v, not its start plus 45 minutes", got, moved)
	}
	own := moved.Add(24 * time.Hour)
	if _, err := w.store.SetOverrides(ctx, req, domain.OverrideInput{
		StudentIDs: []string{overridden}, ClosesAt: &own, DurationMin: ptr(5), Reason: "đi xa", Now: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	if got := w.deadlineOf(t, overriddenAttempt); !got.Equal(own) {
		t.Errorf("the overridden flexible attempt has deadline %v, want the override's close %v", got, own)
	}
}

// The student's card and intro carry the mode.
func TestTheStudentsCardAndIntroCarryTheMode(t *testing.T) {
	w := newReachWorld(t)
	in := input(w.versionA, []string{w.classA}, nil)
	in.Mode = schedule.Flexible
	flexible := w.create(t, as(w.a, false), in)
	sections, err := w.store.ForStudent(context.Background(), w.studentA, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	modes := map[string]domain.Mode{}
	for _, c := range sections.DueNow {
		modes[c.ID] = c.Mode
	}
	if modes[flexible] != schedule.Flexible || modes[w.mineA] != schedule.Strict {
		t.Errorf("the cards read %v, want %s flexible and %s strict", modes, flexible, w.mineA)
	}
	detail, err := w.store.StudentDetail(context.Background(), flexible, w.studentA)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Mode != schedule.Flexible {
		t.Errorf("the intro reads %q", detail.Mode)
	}
}
