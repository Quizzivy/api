package application_test

import (
	"context"
	"errors"
	"testing"
	"time"

	messagingquery "quizzivy/internal/modules/messaging/application/query"
	"quizzivy/internal/modules/notifications/application/command"
	"quizzivy/internal/modules/notifications/application/query"
	"quizzivy/internal/modules/notifications/domain"
	"quizzivy/internal/shared/cqrs"
)

func TestTheSummaryCountsAStudentsMessagesOnlyWhenAsked(t *testing.T) {
	ctx := context.Background()
	store := only(t, "Unread")
	store.unread = 2
	got, err := over(store).Queries.Summary.Handle(ctx, query.Summary{UserID: reader, Messages: true})
	if err != nil || got != (domain.Summary{UnreadNotifications: 2}) {
		t.Errorf("without the messaging port the summary is %+v (%v), want no message counted", got, err)
	}

	var asked []messagingquery.StudentUnread
	app := over(store).WithUnreadMessages(cqrs.HandlerFunc[messagingquery.StudentUnread, int](func(_ context.Context, q messagingquery.StudentUnread) (int, error) {
		asked = append(asked, q)
		return 7, nil
	}))
	if got, err := app.Queries.Summary.Handle(ctx, query.Summary{UserID: reader}); err != nil || got.UnreadMessages != 0 || len(asked) != 0 {
		t.Errorf("a summary that did not ask for messages is %+v (%v) and asked %v", got, err, asked)
	}
	got, err = app.Queries.Summary.Handle(ctx, query.Summary{UserID: reader, Audience: domain.AudienceStudent, Messages: true})
	if err != nil || got != (domain.Summary{UnreadNotifications: 2, UnreadMessages: 7}) || len(asked) != 1 || asked[0].StudentID != reader {
		t.Errorf("the summary is %+v (%v) after asking %v, want both counts for the caller", got, err, asked)
	}

	expected := errors.New("messaging unavailable")
	failing := over(only(t, "Unread")).WithUnreadMessages(cqrs.HandlerFunc[messagingquery.StudentUnread, int](func(context.Context, messagingquery.StudentUnread) (int, error) {
		return 0, expected
	}))
	if _, err := failing.Queries.Summary.Handle(ctx, query.Summary{UserID: reader, Messages: true}); !errors.Is(err, expected) {
		t.Errorf("a failing count gave %v", err)
	}
}

func TestMarkKeyReadHandsTheStoreItsMoment(t *testing.T) {
	at := time.Date(2026, 11, 17, 2, 0, 0, 0, time.UTC)
	store := only(t, "MarkKeyRead")
	if _, err := over(store).Commands.MarkKeyRead.Handle(context.Background(), command.MarkKeyRead{UserID: reader, DedupeKey: domain.MessageKey(assignment), WrittenBy: at}); err != nil {
		t.Fatal(err)
	}
	if store.userID != reader || store.key != "msg:"+assignment || !store.written.Equal(at) {
		t.Errorf("the store was asked for %s %s %v", store.userID, store.key, store.written)
	}
	for name, cmd := range map[string]command.MarkKeyRead{"no user": {DedupeKey: "msg:x"}, "no key": {UserID: reader}} {
		store := only(t, "")
		if _, err := over(store).Commands.MarkKeyRead.Handle(context.Background(), cmd); err == nil || store.calls != 0 {
			t.Errorf("%s: err = %v after %d calls, want a refusal before the store", name, err, store.calls)
		}
	}
}
