//go:build integration

package email_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"quizzivy/internal/platform/email"
)

// preparing is a Preparer for attempt_flagged that records what it was shown
// and answers as told.
type preparing struct {
	mu     sync.Mutex
	drafts []email.Draft
	send   bool
	err    error
}

func (p *preparing) Prepare(_ context.Context, d email.Draft) (email.Params, bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.drafts = append(p.drafts, d)
	if p.err != nil || !p.send {
		return nil, false, p.err
	}
	params := d.Params.(email.AttemptFlaggedParams)
	params.FullName, params.StudentName, params.Title, params.FocusLost = "Cô Lan", "Lê Hoàng Nam", "Đề giữa kỳ", 2
	return params, true, nil
}

func flagged(to, userID, attemptID string) email.Mail {
	return email.Mail{To: to, ToUserID: userID, Params: email.AttemptFlaggedParams{AttemptID: attemptID}}
}

// TestAPreparerFillsTheIdsOnlyRowBeforeItIsRendered fails when the dispatcher
// renders a Prepared template from its stored ids, without its Preparer, or
// hands the Preparer less than the row's recipient, params and time.
func TestAPreparerFillsTheIdsOnlyRowBeforeItIsRendered(t *testing.T) {
	w := newWorld(t, day(20))
	p := &preparing{send: true}
	w.disp.Prepare = email.Preparers{email.AttemptFlagged: p}
	to, user := addr(t, "prepared"), newUser(t, w.pool, "vi")
	id := w.enqueue(t, flagged(to, user, someAttempt))
	if _, err := w.disp.Dispatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	if r := read(t, w.pool, id); r.sentAt == nil {
		t.Fatalf("the prepared row was not sent: %+v", r)
	}
	sent := w.sender.to(to)
	if len(sent) != 1 || !strings.Contains(sent[0].Text, "Lê Hoàng Nam") || !strings.Contains(sent[0].Subject, "Đề giữa kỳ") ||
		!strings.Contains(sent[0].Text, staffHost+"/teacher/attempts/"+someAttempt) {
		t.Fatalf("the message was not rendered from the prepared params: %+v", sent)
	}
	var mine *email.Draft
	for i := range p.drafts {
		if p.drafts[i].ToUserID == user {
			mine = &p.drafts[i]
		}
	}
	if mine == nil || mine.Params.(email.AttemptFlaggedParams).AttemptID != someAttempt || mine.CreatedAt.IsZero() || !mine.Now.Equal(day(20)) {
		t.Errorf("the Preparer saw %+v", mine)
	}
	var stored string
	if err := w.pool.QueryRow(context.Background(), `SELECT params::text FROM app.email_outbox WHERE id = $1::uuid`, id).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != `{"attemptId": "`+someAttempt+`"}` {
		t.Errorf("the row holds %s, want the attempt's id alone", stored)
	}
}

// TestARowItsPreparerDropsIsClosedAsSkippedAndNeverSent fails when a dropped
// row is sent, retried, left pending, or closed under another code than
// SKIPPED, which R5's housekeeping prunes like any failed row.
func TestARowItsPreparerDropsIsClosedAsSkippedAndNeverSent(t *testing.T) {
	w := newWorld(t, day(21))
	w.disp.Prepare = email.Preparers{email.AttemptFlagged: &preparing{send: false}}
	to := addr(t, "dropped")
	id := w.enqueue(t, flagged(to, newUser(t, w.pool, "vi"), someAttempt))
	batch, err := w.disp.Dispatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	r := read(t, w.pool, id)
	if r.sentAt != nil || r.failedAt == nil || r.lastError != email.SkippedCode || r.attempts != 0 {
		t.Errorf("the dropped row is %+v, want failed with SKIPPED and no attempt spent", r)
	}
	if len(w.sender.to(to)) != 0 || batch.Skipped < 1 {
		t.Errorf("the dropped row was sent, or not counted as skipped: %+v", batch)
	}
	if again, err := w.disp.Dispatch(context.Background()); err != nil || len(w.sender.to(to)) != 0 {
		t.Errorf("a second dispatch sent the dropped row: %+v %v", again, err)
	}
}

// TestAPreparedTemplateWithoutItsPreparerWaitsForADispatcherThatHasIt fails
// when a dispatcher without a template's Preparer claims its rows (it could
// only render them from ids) or wakes for them.
func TestAPreparedTemplateWithoutItsPreparerWaitsForADispatcherThatHasIt(t *testing.T) {
	w := newWorld(t, day(22))
	to := addr(t, "waiting")
	id := w.enqueue(t, flagged(to, newUser(t, w.pool, "vi"), someAttempt))
	if _, err := w.disp.Dispatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	if r := read(t, w.pool, id); r.sentAt != nil || r.failedAt != nil || r.attempts != 0 {
		t.Fatalf("a dispatcher without the Preparer touched the row: %+v", r)
	}
	if due, pending, err := w.disp.NextDue(context.Background()); err != nil || pending && due.Equal(read(t, w.pool, id).deliverAfter) {
		t.Errorf("NextDue counts the row it cannot send: %v %v %v", due, pending, err)
	}
	w.disp.Prepare = email.Preparers{email.AttemptFlagged: &preparing{send: true}}
	if _, err := w.disp.Dispatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	if r := read(t, w.pool, id); r.sentAt == nil {
		t.Errorf("the dispatcher with the Preparer did not send the row: %+v", r)
	}
}

func TestAFailingPreparerIsRetriedAsATransientFailure(t *testing.T) {
	w := newWorld(t, day(23))
	w.disp.Prepare = email.Preparers{email.AttemptFlagged: &preparing{err: errors.New("database gone")}}
	to := addr(t, "retried")
	id := w.enqueue(t, flagged(to, newUser(t, w.pool, "vi"), someAttempt))
	if _, err := w.disp.Dispatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	r := read(t, w.pool, id)
	if r.sentAt != nil || r.failedAt != nil || r.attempts != 1 || r.lastError != "prepare_failed" || !r.deliverAfter.Equal(day(23).Add(time.Minute)) {
		t.Errorf("a failed Prepare left %+v, want a retry in a minute", r)
	}
}

// TestTheDailyBudgetStillDefersPreparedRowsAndPrepareDropsThoseStaleByMorning
// fails when the notification emails escape EMAIL_DAILY_LIMIT, or when a row
// the cap moved to the next local midnight is sent although its Preparer
// finds it stale then, such as a reminder for an assignment that closed.
func TestTheDailyBudgetStillDefersPreparedRowsAndPrepareDropsThoseStaleByMorning(t *testing.T) {
	w := newWorld(t, day(24))
	w.disp.DailyLimit = 1
	p := &preparing{send: true}
	w.disp.Prepare = email.Preparers{email.AttemptFlagged: p}
	user := newUser(t, w.pool, "vi")
	first, second := w.enqueue(t, flagged(addr(t, "first"), user, someAttempt)), w.enqueue(t, flagged(addr(t, "second"), user, someAssignment))
	for range 2 {
		if _, err := w.disp.Dispatch(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	midnight := time.Date(2031, 3, 25, 0, 0, 0, 0, time.FixedZone("ICT", 7*3600))
	a, b := read(t, w.pool, first), read(t, w.pool, second)
	if (a.sentAt == nil) == (b.sentAt == nil) {
		t.Fatalf("with a budget of one, %+v and %+v were both sent or both held", a, b)
	}
	held := second
	if a.sentAt == nil {
		held = first
	}
	if r := read(t, w.pool, held); !r.deliverAfter.Equal(midnight) {
		t.Fatalf("the row over the budget waits until %v, want %v", r.deliverAfter, midnight)
	}
	p.mu.Lock()
	p.send = false
	p.mu.Unlock()
	w.disp.Now = func() time.Time { return midnight.Add(time.Minute) }
	if _, err := w.disp.Dispatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	if r := read(t, w.pool, held); r.sentAt != nil || r.lastError != email.SkippedCode {
		t.Errorf("the held row that went stale overnight is %+v, want skipped", r)
	}
}
