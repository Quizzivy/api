package application_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"quizzivy/internal/modules/notifications/application/command"
	"quizzivy/internal/modules/notifications/domain"
)

func submitted(merge domain.Merge) command.Notify {
	return command.Notify{
		UserID:    reader,
		Kind:      domain.AttemptSubmitted,
		Params:    domain.Submitted{Title: "Đề giữa kỳ", Count: 1, ToGrade: 1},
		Target:    &domain.Target{Route: domain.RouteAssignment, AssignmentID: assignment},
		DedupeKey: "submitted:" + assignment + ":1958400",
		Merge:     merge,
	}
}

func TestNotifyWritesOnceAndNeverReads(t *testing.T) {
	for _, merge := range []domain.Merge{domain.Replace, domain.Add, domain.Renew} {
		store := only(t, "Upsert")
		cmd := submitted(merge)
		if _, err := over(store).Commands.Notify.Handle(context.Background(), cmd); err != nil {
			t.Fatalf("merge %d: %v", merge, err)
		}
		if store.calls != 1 {
			t.Errorf("merge %d: the store was called %d times, want exactly one write", merge, store.calls)
		}
		got := store.notice
		if got.Merge != merge {
			t.Errorf("the store was handed merge %d, want %d", got.Merge, merge)
		}
		if got.Params != cmd.Params {
			t.Errorf("the store was handed params %+v, want them unchanged: %+v", got.Params, cmd.Params)
		}
		if got.UserID != cmd.UserID || got.Kind != cmd.Kind || got.DedupeKey != cmd.DedupeKey || got.Target != cmd.Target {
			t.Errorf("the store was handed %+v, want the command's own fields", got)
		}
	}
}

func TestNotifyRefusesAnUnknownMergeMode(t *testing.T) {
	for _, merge := range []domain.Merge{0, domain.Renew + 1, -1} {
		store := only(t, "")
		_, err := over(store).Commands.Notify.Handle(context.Background(), submitted(merge))
		if !errors.Is(err, domain.ErrUnknownMerge) {
			t.Errorf("merge %d: err = %v, want ErrUnknownMerge", merge, err)
		}
		if store.calls != 0 {
			t.Errorf("merge %d: the store was called for a notice that was refused", merge)
		}
	}
}

func TestNotifyRefusesANoticeTheStoreCannotWrite(t *testing.T) {
	for label, c := range map[string]struct {
		change func(*command.Notify)
		want   error
	}{
		"no recipient":            {func(n *command.Notify) { n.UserID = "" }, domain.ErrNoRecipient},
		"an unknown kind":         {func(n *command.Notify) { n.Kind = "content.unknown" }, domain.ErrUnknownKind},
		"another kind's params":   {func(n *command.Notify) { n.Params = domain.Ready{Title: "Đề giữa kỳ"} }, domain.ErrParamsMismatch},
		"params out of bounds":    {func(n *command.Notify) { n.Params = domain.Submitted{Count: 1} }, domain.ErrInvalidParams},
		"a target without its id": {func(n *command.Notify) { n.Target = &domain.Target{Route: domain.RouteAttempt} }, domain.ErrInvalidTarget},
		"no dedupe key":           {func(n *command.Notify) { n.DedupeKey = "" }, domain.ErrInvalidDedupeKey},
	} {
		store := only(t, "")
		cmd := submitted(domain.Add)
		c.change(&cmd)
		if _, err := over(store).Commands.Notify.Handle(context.Background(), cmd); !errors.Is(err, c.want) {
			t.Errorf("%s: err = %v, want %v", label, err, c.want)
		}
		if store.calls != 0 {
			t.Errorf("%s: the store was called for a notice that was refused", label)
		}
	}
}

type mailer struct {
	store   *fakeStore
	notices []domain.Notice
	err     error
}

func (m *mailer) Mail(_ context.Context, n domain.Notice, now time.Time) error {
	if m.store.calls != 1 || now.IsZero() {
		m.store.t.Errorf("the email channel ran before the in-app write, or without a clock (%d writes, %v)", m.store.calls, now)
	}
	m.notices = append(m.notices, n)
	return m.err
}

// TestNotifyHandsEachWrittenNoticeToTheEmailChannel fails when Notify sends a
// notice's email before its in-app write, sends one for a notice it refused,
// or hides the email channel's failure.
func TestNotifyHandsEachWrittenNoticeToTheEmailChannel(t *testing.T) {
	store := only(t, "Upsert")
	m := &mailer{store: store}
	app := over(store).WithMail(m)
	cmd := submitted(domain.Add)
	if _, err := app.Commands.Notify.Handle(context.Background(), cmd); err != nil {
		t.Fatal(err)
	}
	if len(m.notices) != 1 || m.notices[0].DedupeKey != cmd.DedupeKey || m.notices[0].UserID != reader {
		t.Errorf("the email channel was handed %+v", m.notices)
	}
	if _, err := app.Commands.Notify.Handle(context.Background(), submitted(0)); err == nil || len(m.notices) != 1 {
		t.Errorf("a refused notice reached the email channel (%v)", err)
	}
	store.calls = 0
	m.err = errors.New("outbox gone")
	if _, err := app.Commands.Notify.Handle(context.Background(), cmd); !errors.Is(err, m.err) {
		t.Errorf("the email channel's failure answered %v", err)
	}
}
