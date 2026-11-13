//go:build integration

package email_test

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"quizzivy/internal/platform/email"
	"quizzivy/internal/shared/access"
)

func userOfRole(t *testing.T, pool *pgxpool.Pool, roleID string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO app.users (email, full_name, role_id, locale)
		VALUES ($1, 'Người nhận thư', $2::uuid, 'vi')
		RETURNING id::text`, "host-"+nonce(t)+"@example.com", roleID).Scan(&id); err != nil {
		t.Fatalf("user: %v", err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM app.users WHERE id = $1::uuid`, id); err != nil {
			t.Errorf("removing the user: %v", err)
		}
	})
	return id
}

func builtinRole(t *testing.T, pool *pgxpool.Pool, key string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(), `SELECT id::text FROM app.roles WHERE builtin_key = $1`, key).Scan(&id); err != nil {
		t.Fatalf("the built-in %s role: %v", key, err)
	}
	return id
}

func customRole(t *testing.T, pool *pgxpool.Pool, keys ...access.Key) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO app.roles (name, icon, color) VALUES ($1, 'star', 'gray') RETURNING id::text`,
		"Vai trò "+nonce(t)).Scan(&id); err != nil {
		t.Fatalf("role: %v", err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM app.roles WHERE id = $1::uuid`, id); err != nil {
			t.Errorf("removing the role: %v", err)
		}
	})
	for _, key := range keys {
		if _, err := pool.Exec(context.Background(),
			`INSERT INTO app.role_permissions (role_id, permission_key) VALUES ($1::uuid, $2)`, id, string(key)); err != nil {
			t.Fatalf("grant %s: %v", key, err)
		}
	}
	return id
}

// TestALinkOpensAtTheHostItsRecipientBelongsAt fails when the dispatcher
// renders every link into one host, and when its SQL classes a role otherwise
// than access.HomeAudience does. No other test sends to recipients of
// different roles.
func TestALinkOpensAtTheHostItsRecipientBelongsAt(t *testing.T) {
	w := newWorld(t, day(21))
	roles := map[string]struct {
		id   string
		keys access.Set
	}{
		"the built-in student":         {builtinRole(t, w.pool, "student"), access.NewSet(access.LearningTakeTests)},
		"the built-in teacher":         {builtinRole(t, w.pool, "teacher"), access.NewSet(access.TeachingClassesWrite)},
		"the built-in admin":           {builtinRole(t, w.pool, "admin"), access.NewSet(access.ScopeAll)},
		"a role that only takes tests": {customRole(t, w.pool, access.LearningTakeTests), access.NewSet(access.LearningTakeTests)},
		"a role with one teaching key": {customRole(t, w.pool, access.LearningTakeTests, access.TeachingClassesWrite), access.NewSet(access.LearningTakeTests, access.TeachingClassesWrite)},
		"a role with one admin key":    {customRole(t, w.pool, access.SystemAuditRead), access.NewSet(access.SystemAuditRead)},
		"a role with no key":           {customRole(t, w.pool), access.NewSet()},
	}
	for name, role := range roles {
		to := addr(t, "host")
		w.enqueue(t, email.Mail{ToUserID: userOfRole(t, w.pool, role.id), To: to, Params: email.SignInInviteParams{}})
		if _, err := w.disp.Dispatch(context.Background()); err != nil {
			t.Fatal(err)
		}
		sent := w.sender.to(to)
		if len(sent) != 1 {
			t.Fatalf("%s: sent %d times, want once", name, len(sent))
		}
		want := studentHost
		if access.HomeAudience(role.keys) == access.AudienceStaff {
			want = staffHost
		}
		if !strings.Contains(sent[0].Text, want+"/login") {
			t.Errorf("%s: the link is not at %s: %s", name, want, sent[0].Text)
		}
	}

	export, stranger := addr(t, "export"), addr(t, "nouser")
	w.enqueue(t, email.Mail{ToUserID: userOfRole(t, w.pool, roles["the built-in student"].id), To: export,
		Params: email.DataExportReadyParams{ExportID: "0192a7c0-0000-7000-8000-000000000001"}})
	w.enqueue(t, email.Mail{To: stranger, Params: email.SignInInviteParams{}})
	if _, err := w.disp.Dispatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	if sent := w.sender.to(export); len(sent) != 1 || !strings.Contains(sent[0].Text, staffHost+"/admin/exports/") {
		t.Errorf("a data export's link is not at the staff host whoever receives it: %+v", sent)
	}
	if sent := w.sender.to(stranger); len(sent) != 1 || !strings.Contains(sent[0].Text, studentHost+"/login") {
		t.Errorf("a message to no account is not at the student host: %+v", sent)
	}
}
