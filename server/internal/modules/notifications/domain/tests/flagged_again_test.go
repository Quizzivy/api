package domain_test

import (
	"testing"
	"time"

	"quizzivy/internal/modules/notifications/domain"
)

// F1: the absences on a flagged flexible attempt are told again under one key
// per fifteen minutes, beside the flag's own key, and each bucket emails once.
func TestTheAbsencesOfAFlaggedAttemptShareAKeyForFifteenMinutes(t *testing.T) {
	quarter := time.Date(2026, 11, 18, 9, 15, 0, 0, time.UTC)
	same := domain.FlaggedAgainKey(someAttempt, quarter)
	if want := domain.FlaggedKey(someAttempt) + ":1994437"; same != want {
		t.Errorf("the key is %q, want flagged:{attemptId}:{unix seconds / 900} = %q", same, want)
	}
	if got := domain.FlaggedAgainKey(someAttempt, quarter.Add(14*time.Minute+59*time.Second)); got != same {
		t.Errorf("the last second of the quarter keys %q", got)
	}
	if got := domain.FlaggedAgainKey(someAttempt, quarter.Add(15*time.Minute)); got == same {
		t.Errorf("the next quarter shares the key %q", got)
	}
	if bucket, ok := domain.FlaggedAgainBucket(same); !ok || bucket != "1994437" {
		t.Errorf("the bucket of %q reads %q, %v", same, bucket, ok)
	}
	if _, ok := domain.FlaggedAgainBucket(domain.FlaggedKey(someAttempt)); ok {
		t.Error("the flag's own key reads as a repeat")
	}

	notice := func(key string) domain.Notice {
		return domain.Notice{UserID: someUser, Kind: domain.AttemptFlagged, Params: domain.Flagged{StudentName: "Nam", Title: "Đề", FocusLost: 2},
			Target: &domain.Target{Route: domain.RouteAttempt, AttemptID: someAttempt, AssignmentID: someAssignment}, DedupeKey: key, Merge: domain.Replace}
	}
	first, ok := domain.PlanEmail(notice(domain.FlaggedKey(someAttempt)), quarter)
	if !ok || first.DedupeKey != "email:flagged:"+someAttempt+":"+someUser {
		t.Fatalf("the flag's email is keyed %q, %v: it must stay as at v0.14.0", first.DedupeKey, ok)
	}
	again, ok := domain.PlanEmail(notice(same), quarter)
	if !ok || again.DedupeKey != first.DedupeKey+":1994437" {
		t.Errorf("a repeat's email is keyed %q, %v, want the flag's and the bucket", again.DedupeKey, ok)
	}
}
