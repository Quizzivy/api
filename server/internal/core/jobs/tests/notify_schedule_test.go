package jobs_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"quizzivy/internal/core/jobs"
)

type scriptedScheduler struct {
	mu     sync.Mutex
	runs   int
	reads  int
	busy   []bool
	errs   []error
	due    func(now time.Time) (time.Time, bool)
	ran    chan struct{}
	readAt chan time.Time
}

func newScriptedScheduler(due func(now time.Time) (time.Time, bool)) *scriptedScheduler {
	return &scriptedScheduler{due: due, ran: make(chan struct{}, 64), readAt: make(chan time.Time, 64)}
}

func (s *scriptedScheduler) Run(context.Context, time.Time) (bool, error) {
	s.mu.Lock()
	n := s.runs
	s.runs++
	var busy bool
	var err error
	if n < len(s.busy) {
		busy = s.busy[n]
	}
	if n < len(s.errs) {
		err = s.errs[n]
	}
	s.mu.Unlock()
	s.ran <- struct{}{}
	return busy, err
}

func (s *scriptedScheduler) NextDue(_ context.Context, now time.Time) (time.Time, bool, error) {
	s.mu.Lock()
	s.reads++
	s.mu.Unlock()
	s.readAt <- now
	at, ok := s.due(now)
	return at, ok, nil
}

func (s *scriptedScheduler) counts() (runs, reads int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.runs, s.reads
}

func runSchedule(t *testing.T, s *scriptedScheduler, schedule jobs.NotifySchedule) (*jobs.Nudge, *housekeepingLog) {
	t.Helper()
	nudge := jobs.NewNudge()
	schedule.Nudge = nudge.C()
	logs := &housekeepingLog{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		jobs.RunNotifySchedule(ctx, logs.logger(), s, schedule)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("the scheduler did not stop with the process")
		}
	})
	return nudge, logs
}

func waitRun(t *testing.T, s *scriptedScheduler, within time.Duration) {
	t.Helper()
	select {
	case <-s.ran:
	case <-time.After(within):
		t.Fatalf("no run within %s", within)
	}
}

func noRun(t *testing.T, s *scriptedScheduler, during time.Duration) {
	t.Helper()
	select {
	case <-s.ran:
		t.Fatalf("a run within %s", during)
	case <-time.After(during):
	}
}

func TestWithNothingDueTheSchedulerRunsAtStartAndThenOnlyForItsIdleCheck(t *testing.T) {
	s := newScriptedScheduler(func(time.Time) (time.Time, bool) { return time.Time{}, false })
	runSchedule(t, s, jobs.NotifySchedule{Idle: 300 * time.Millisecond, Retry: time.Hour, Busy: time.Hour, Floor: time.Millisecond})
	waitRun(t, s, time.Second)
	noRun(t, s, 200*time.Millisecond)
	waitRun(t, s, time.Second)
	if runs, reads := s.counts(); runs != 2 || reads < 1 {
		t.Errorf("runs=%d reads=%d, want two runs: at start and after the idle check", runs, reads)
	}
}

func TestTheSchedulerSleepsUntilTheNextMomentItReadsAndNoLonger(t *testing.T) {
	start := time.Now()
	s := newScriptedScheduler(func(now time.Time) (time.Time, bool) { return now.Add(150 * time.Millisecond), true })
	runSchedule(t, s, jobs.NotifySchedule{Idle: time.Hour, Retry: time.Hour, Busy: time.Hour, Floor: time.Millisecond})
	waitRun(t, s, time.Second)
	waitRun(t, s, time.Second)
	if elapsed := time.Since(start); elapsed < 150*time.Millisecond || elapsed > 900*time.Millisecond {
		t.Errorf("the second run came after %s, want about 150ms", elapsed)
	}
}

func TestAMomentFurtherAwayThanTheIdleCheckWaitsOnlyForTheIdleCheck(t *testing.T) {
	s := newScriptedScheduler(func(now time.Time) (time.Time, bool) { return now.Add(time.Hour), true })
	runSchedule(t, s, jobs.NotifySchedule{Idle: 100 * time.Millisecond, Retry: time.Hour, Busy: time.Hour, Floor: time.Millisecond})
	waitRun(t, s, time.Second)
	waitRun(t, s, time.Second)
}

func TestANudgeRunsAndRecomputesAtOnce(t *testing.T) {
	s := newScriptedScheduler(func(time.Time) (time.Time, bool) { return time.Time{}, false })
	nudge, _ := runSchedule(t, s, jobs.NotifySchedule{Idle: time.Hour, Retry: time.Hour, Busy: time.Hour, Floor: time.Millisecond})
	waitRun(t, s, time.Second)
	<-s.readAt
	nudge.Nudge()
	waitRun(t, s, time.Second)
	select {
	case <-s.readAt:
	case <-time.After(time.Second):
		t.Fatal("the nudged run did not read its next moment again")
	}
	noRun(t, s, 100*time.Millisecond)
}

func TestARunAnotherMachineHeldIsTriedAgainAfterTheBusyPause(t *testing.T) {
	s := newScriptedScheduler(func(time.Time) (time.Time, bool) { return time.Time{}, false })
	s.busy = []bool{true}
	runSchedule(t, s, jobs.NotifySchedule{Idle: time.Hour, Retry: time.Hour, Busy: 50 * time.Millisecond, Floor: time.Millisecond})
	waitRun(t, s, time.Second)
	waitRun(t, s, time.Second)
	if _, reads := s.counts(); reads != 1 {
		t.Errorf("reads=%d: a busy run read its next moment, want the second run alone to", reads)
	}
}

func TestAFailedRunIsLoggedByItsCodeAndRetried(t *testing.T) {
	s := newScriptedScheduler(func(time.Time) (time.Time, bool) { return time.Time{}, false })
	s.errs = []error{errors.New("connection refused to postgres://quizzivy:secret@db")}
	_, logs := runSchedule(t, s, jobs.NotifySchedule{Idle: time.Hour, Retry: 50 * time.Millisecond, Busy: time.Hour, Floor: time.Millisecond})
	waitRun(t, s, time.Second)
	waitRun(t, s, time.Second)
	text := logs.text()
	if !strings.Contains(text, "NOTIFY_SCHEDULE_FAILED") || strings.Contains(text, "secret") {
		t.Errorf("logs %s, want the code and not the error's text", text)
	}
}

func TestANudgeNeverBlocksTheWriteThatSendsIt(t *testing.T) {
	nudge := jobs.NewNudge()
	done := make(chan struct{})
	go func() {
		for range 100 {
			nudge.Nudge()
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("a nudge blocked with nobody listening")
	}
	<-nudge.C()
	select {
	case <-nudge.C():
		t.Fatal("a hundred nudges were held as more than one")
	default:
	}
}
