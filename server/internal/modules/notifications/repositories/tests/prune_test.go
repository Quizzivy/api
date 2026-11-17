//go:build integration

package repositories_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"quizzivy/internal/modules/notifications/application/command"
	"quizzivy/internal/modules/notifications/domain"
)

func TestThePruneDeletesWhatWasFirstWrittenMoreThan180DaysAgo(t *testing.T) {
	pool := newPool(t)
	app := over(pool)
	userID, other := newUser(t, pool, "teacher"), newUser(t, pool, "student")
	now := time.Now().UTC().Truncate(time.Microsecond)
	app.SetClock(func() time.Time { return now })
	day := 24 * time.Hour
	cutoff := now.Add(-180 * day)

	ages := map[string]time.Time{
		"181 days":            now.Add(-181 * day),
		"a moment past 180":   cutoff.Add(-time.Microsecond),
		"exactly 180 days":    cutoff,
		"a moment within 180": cutoff.Add(time.Microsecond),
		"179 days":            now.Add(-179 * day),
		"today":               now,
	}
	kept := []string{"exactly 180 days", "a moment within 180", "179 days", "today"}
	ids := map[string]string{}
	for label, createdAt := range ages {
		for _, owner := range []string{userID, other} {
			var id string
			if err := pool.QueryRow(context.Background(), `
				INSERT INTO app.notifications (user_id, kind, params, dedupe_key, created_at, read_at)
				VALUES ($1::uuid, 'result.ready', '{"title":"Đề giữa kỳ"}'::jsonb, $2, $3, $3)
				RETURNING id::text`, owner, label, createdAt).Scan(&id); err != nil {
				t.Fatalf("%s: %v", label, err)
			}
			if owner == userID {
				ids[id] = label
			}
		}
	}

	deleted, err := app.Commands.Prune.Handle(context.Background(), command.Prune{})
	if err != nil {
		t.Fatal(err)
	}
	if deleted < 4 {
		t.Errorf("the prune reports %d rows, want at least the four this test aged past 180 days", deleted)
	}
	for _, owner := range []string{userID, other} {
		var left []string
		for _, r := range rowsOf(t, pool, owner) {
			if owner == userID {
				left = append(left, ids[r.id])
			} else {
				left = append(left, r.createdAt.UTC().Format(time.RFC3339Nano))
			}
		}
		if owner == userID {
			slices.Sort(left)
			want := slices.Clone(kept)
			slices.Sort(want)
			if !slices.Equal(left, want) {
				t.Errorf("the prune left %v, want %v", left, want)
			}
		} else if len(left) != len(kept) {
			t.Errorf("the prune left another user %d rows, want %d: %v", len(left), len(kept), left)
		}
	}

	again, err := app.Commands.Prune.Handle(context.Background(), command.Prune{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rowsOf(t, pool, userID)) != len(kept) {
		t.Errorf("a second prune removed more rows (%d reported)", again)
	}
}

// TestARenewedItemIsKeptFromItsLatestWrite fails when the prune counts a
// Renew item's 180 days from its first write, or when a Replace or Add merge
// extends an item's.
func TestARenewedItemIsKeptFromItsLatestWrite(t *testing.T) {
	pool := newPool(t)
	app := over(pool)
	userID := newUser(t, pool, "teacher")
	old := time.Now().Add(-181 * 24 * time.Hour)
	keys := map[domain.Merge]string{domain.Renew: domain.MessageKey(uuid.NewString()), domain.Replace: "joined:" + nonce(t), domain.Add: "graded:" + nonce(t)}
	params := map[domain.Merge]domain.Params{
		domain.Renew:   said("Lê Hoàng Nam", "Dạ em chào cô"),
		domain.Replace: domain.Joined{StudentName: "Nguyễn Gia Bảo", ClassName: "Lớp 5A"},
		domain.Add:     domain.Joined{StudentName: "Nguyễn Gia Bảo", ClassName: "Lớp 5B"},
	}
	for merge, key := range keys {
		notify(t, app, userID, key, params[merge], merge)
		if _, err := pool.Exec(context.Background(), `UPDATE app.notifications SET created_at = $3 WHERE user_id = $1::uuid AND dedupe_key = $2`, userID, key, old); err != nil {
			t.Fatal(err)
		}
		notify(t, app, userID, key, params[merge], merge)
	}
	if _, err := app.Commands.Prune.Handle(context.Background(), command.Prune{}); err != nil {
		t.Fatal(err)
	}
	left := rowsOf(t, pool, userID)
	if len(left) != 1 || left[0].kind != string(domain.MessageReceived) {
		t.Errorf("the prune left %+v, want the renewed message item alone", left)
	}
}
