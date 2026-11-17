//go:build integration

package repositories_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"quizzivy/internal/modules/notifications/application"
	"quizzivy/internal/modules/notifications/application/command"
	"quizzivy/internal/modules/notifications/domain"
	"quizzivy/internal/modules/notifications/repositories"
	"quizzivy/internal/platform/db"
)

func said(name, excerpt string) domain.MessageNotice {
	return domain.MessageNotice{SenderName: name, Count: 1, Excerpt: excerpt}
}

// TestRenewCountsWhileUnreadAndStartsAgainOnceRead fails when a renewed item
// keeps its id or its creation, counts past a read, or takes anything but
// the count from the stored row.
func TestRenewCountsWhileUnreadAndStartsAgainOnceRead(t *testing.T) {
	pool := newPool(t)
	app := over(pool)
	userID := newUser(t, pool, "teacher")
	key := domain.MessageKey(uuid.NewString())
	notify(t, app, userID, key, said("Lê Hoàng Nam", "Dạ em chào cô"), domain.Renew)
	first := theRow(t, pool, userID)
	if first.params["count"] != float64(1) || first.readAt != nil {
		t.Fatalf("the first message stored %v read at %v, want an unread count of one", first.params, first.readAt)
	}
	time.Sleep(20 * time.Millisecond)
	notify(t, app, userID, key, said("Lê Hoàng Nam", "Em gửi bài ạ"), domain.Renew)
	second := theRow(t, pool, userID)
	if second.params["count"] != float64(2) || second.params["excerpt"] != "Em gửi bài ạ" {
		t.Errorf("a second message while unread stored %v, want a count of two and the newest excerpt", second.params)
	}
	if second.id <= first.id || !second.createdAt.After(first.createdAt) {
		t.Errorf("the renewed item is %s at %v, was %s at %v: it must list first", second.id, second.createdAt, first.id, first.createdAt)
	}

	mark(t, app, userID, second.id)
	notify(t, app, userID, key, said("Lê Hoàng Nam", "Cô ơi"), domain.Renew)
	third := theRow(t, pool, userID)
	if third.params["count"] != float64(1) || third.readAt != nil || third.id == second.id {
		t.Errorf("a message after a read stored %v read at %v as %s, want a new unread item counting one", third.params, third.readAt, third.id)
	}
	if got := unread(t, app, userID); got != 1 {
		t.Errorf("%d unread, want the one item", got)
	}
}

// TestConcurrentRenewalsLoseNoMessage fails when two messages renewing one
// item at once count as one.
func TestConcurrentRenewalsLoseNoMessage(t *testing.T) {
	const writers = 8
	ctx := context.Background()
	shared := newPool(t)
	config := shared.Config().Copy()
	config.MaxConns = writers
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	userID := newUser(t, pool, "student")
	key := domain.MessageKey(uuid.NewString())
	conns := make([]*pgxpool.Conn, writers)
	for i := range conns {
		if conns[i], err = pool.Acquire(ctx); err != nil {
			t.Fatalf("connection %d: %v", i, err)
		}
	}
	start := make(chan struct{})
	failures := make(chan error, writers)
	var done sync.WaitGroup
	for _, conn := range conns {
		done.Go(func() {
			defer conn.Release()
			app := application.New(repositories.NewPostgres(db.NewContext(conn)))
			<-start
			_, err := app.Commands.Notify.Handle(ctx, command.Notify{
				UserID: userID, Kind: domain.MessageFromTeacher, Params: domain.TeacherMessageNotice{SenderName: "Cô Thương", Count: 1, Excerpt: "Chào lớp"},
				Target: &domain.Target{Route: domain.RouteStudentMessages, ClassID: uuid.NewString()}, DedupeKey: key, Merge: domain.Renew,
			})
			failures <- err
		})
	}
	close(start)
	done.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Errorf("a concurrent renewal failed: %v", err)
		}
	}
	if row := theRow(t, pool, userID); row.params["count"] != float64(writers) {
		t.Errorf("%d messages counted %v, want %d", writers, row.params["count"], writers)
	}
}

// TestMarkKeyReadKeepsANoticeWrittenAfterTheMoment fails when the bell clears
// an item a message rang after the reader's pointer was judged, clears
// another key's or another user's item, or moves a first read.
func TestMarkKeyReadKeepsANoticeWrittenAfterTheMoment(t *testing.T) {
	pool := newPool(t)
	app := over(pool)
	userID, other := newUser(t, pool, "teacher"), newUser(t, pool, "teacher")
	key, elsewhere := domain.MessageKey(uuid.NewString()), domain.MessageKey(uuid.NewString())
	var before time.Time
	if err := pool.QueryRow(context.Background(), `SELECT statement_timestamp()`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	notify(t, app, userID, key, said("Lê Hoàng Nam", "Dạ em chào cô"), domain.Renew)
	notify(t, app, userID, elsewhere, said("Trần Thu Hà", "Em hỏi bài"), domain.Renew)
	notify(t, app, other, key, said("Lê Hoàng Nam", "Dạ em chào cô"), domain.Renew)
	clear := func(writtenBy time.Time) {
		t.Helper()
		if _, err := app.Commands.MarkKeyRead.Handle(context.Background(), command.MarkKeyRead{UserID: userID, DedupeKey: key, WrittenBy: writtenBy}); err != nil {
			t.Fatal(err)
		}
	}
	clear(before)
	if got := unread(t, app, userID); got != 2 {
		t.Errorf("a moment before the message cleared its item: %d unread, want 2", got)
	}
	clear(time.Now().Add(time.Minute))
	read := map[string]*time.Time{}
	for _, row := range rowsOf(t, pool, userID) {
		read[row.params["senderName"].(string)] = row.readAt
	}
	if read["Lê Hoàng Nam"] == nil || read["Trần Thu Hà"] != nil {
		t.Errorf("after a moment past the message the items are read at %v, want only the thread's item read", read)
	}
	if got := unread(t, app, other); got != 1 {
		t.Errorf("another user's item under the same key was cleared: %d unread, want 1", got)
	}
	first := *read["Lê Hoàng Nam"]
	clear(time.Now().Add(time.Hour))
	for _, row := range rowsOf(t, pool, userID) {
		if row.params["senderName"] == "Lê Hoàng Nam" && !row.readAt.Equal(first) {
			t.Errorf("clearing a read item moved its read_at from %v to %v", first, row.readAt)
		}
	}
	for name, cmd := range map[string]command.MarkKeyRead{
		"no user": {DedupeKey: key}, "no key": {UserID: userID}, "a key too long": {UserID: userID, DedupeKey: string(make([]byte, domain.MaxDedupeKey+1))},
	} {
		if _, err := app.Commands.MarkKeyRead.Handle(context.Background(), cmd); err == nil {
			t.Errorf("%s: the command was taken", name)
		}
	}
}
