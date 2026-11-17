//go:build integration

package application_test

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"quizzivy/internal/core/adapters"
	notificationsapp "quizzivy/internal/modules/notifications/application"
	notificationsrepo "quizzivy/internal/modules/notifications/repositories"
	questionsapp "quizzivy/internal/modules/questions/application"
	questionsrepo "quizzivy/internal/modules/questions/repositories"
	"quizzivy/internal/modules/sharing/application"
	"quizzivy/internal/modules/sharing/application/command"
	"quizzivy/internal/modules/sharing/domain"
	sharingrepo "quizzivy/internal/modules/sharing/repositories"
	testsapp "quizzivy/internal/modules/tests/application"
	testsrepo "quizzivy/internal/modules/tests/repositories"
	"quizzivy/internal/platform/db"
	"quizzivy/internal/shared/access"
	"quizzivy/internal/shared/actor"
)

type world struct {
	t      *testing.T
	tx     pgx.Tx
	app    *application.Application
	marker string
}

type photos struct{}

func (photos) AvatarURL(_ context.Context, key string) (string, error) {
	return "https://photos.example/" + key, nil
}

func newWorld(t *testing.T) *world {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	tx, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	dbx := db.NewContext(tx)
	questions := questionsrepo.NewPostgres(dbx)
	sets := questionsapp.New(questions, nil).WithSets(questions, questions)
	tests := testsapp.New(testsrepo.NewPostgres(dbx, nil, nil))
	notes := notificationsapp.New(notificationsrepo.NewPostgres(dbx))
	app := application.New(sharingrepo.NewPostgres(dbx), adapters.SharedContent{Tests: tests.Queries.Ownership, Sets: sets.Queries.GetSet}).
		WithAvatars(photos{}).
		WithNotifier(notes.Commands.Notify, nil)
	return &world{t: t, tx: tx, app: app, marker: "zq" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")}
}

func (w *world) id(sql string, args ...any) string {
	w.t.Helper()
	var id string
	if err := w.tx.QueryRow(context.Background(), sql, args...).Scan(&id); err != nil {
		w.t.Fatalf("%s: %v", sql, err)
	}
	return id
}

func (w *world) exec(sql string, args ...any) {
	w.t.Helper()
	if _, err := w.tx.Exec(context.Background(), sql, args...); err != nil {
		w.t.Fatalf("%s: %v", sql, err)
	}
}

func (w *world) user(name, builtin string) string {
	w.t.Helper()
	return w.id(`INSERT INTO app.users (email, full_name, role_id, avatar_key)
		VALUES ($1, $2, (SELECT id FROM app.roles WHERE builtin_key = $3), 'avatars/' || $1) RETURNING id::text`,
		w.marker+"-"+uuid.NewString()[:8]+"@example.test", name+" "+w.marker, builtin)
}

func (w *world) disable(user string) {
	w.t.Helper()
	w.exec(`UPDATE app.users SET disabled_at = now() WHERE id = $1::uuid`, user)
}

func (w *world) test(owner, title string) string {
	w.t.Helper()
	return w.id(`INSERT INTO app.tests (title, created_by, owner_id) VALUES ($1, $2::uuid, $2::uuid) RETURNING id::text`, title, owner)
}

func (w *world) set(owner, name string, live, deleted int) string {
	w.t.Helper()
	set := w.id(`INSERT INTO app.question_sets (owner_id, name, created_by) VALUES ($1::uuid, $2, $1::uuid) RETURNING id::text`, owner, name)
	for i := range live + deleted {
		question := w.id(`INSERT INTO app.questions (type, prompt, points, owner_id, created_by) VALUES ('short_answer', 'Câu hỏi?', 1, $1::uuid, $1::uuid) RETURNING id::text`, owner)
		w.exec(`INSERT INTO app.question_set_items (set_id, question_id, ordinal, added_by) VALUES ($1::uuid, $2::uuid, $3, $4::uuid)`, set, question, i, owner)
		if i >= live {
			w.exec(`UPDATE app.questions SET deleted_at = now() WHERE id = $1::uuid`, question)
		}
	}
	return set
}

func by(id string) actor.Actor {
	return actor.Actor{ID: id, IP: "203.0.113.7", UserAgent: "sharing-test", Scope: access.Scope{UserID: id}}
}

func byAdmin(id string) actor.Actor {
	a := by(id)
	a.Scope.All = true
	return a
}

func (w *world) share(who actor.Actor, kind domain.Kind, resource string, access domain.Access, grantees ...string) domain.Granted {
	w.t.Helper()
	granted, err := w.app.Commands.CreateShares.Handle(context.Background(), command.CreateShares{
		Actor: who, Kind: kind, ResourceID: resource, GranteeIDs: grantees, Access: access,
	})
	if err != nil {
		w.t.Fatalf("%s sharing %s %s: %v", who.ID, kind, resource, err)
	}
	return granted
}

type audited struct {
	Action, Entity, Actor string
	Diff                  map[string]any
	Raw                   string
}

func (w *world) audits(resource string) []audited {
	w.t.Helper()
	rows, err := w.tx.Query(context.Background(), `SELECT action, entity, actor_user_id::text, diff::text
		  FROM app.audit_log WHERE entity_id = $1::uuid AND action LIKE 'share.%' ORDER BY id`, resource)
	if err != nil {
		w.t.Fatal(err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (audited, error) {
		var a audited
		if err := row.Scan(&a.Action, &a.Entity, &a.Actor, &a.Raw); err != nil {
			return a, err
		}
		return a, json.Unmarshal([]byte(a.Raw), &a.Diff)
	})
	if err != nil {
		w.t.Fatal(err)
	}
	return out
}

func sharedWith(before, after any) map[string]any {
	return map[string]any{"shared_with": map[string]any{"old": before, "new": after}}
}

func grantee(id string, access domain.Access) map[string]any {
	return map[string]any{"granteeId": id, "access": string(access)}
}

func (w *world) notices(user string) []string {
	w.t.Helper()
	rows, err := w.tx.Query(context.Background(), `SELECT kind || ' ' || params::text || ' ' || coalesce(target::text, '') || ' ' || dedupe_key
		  FROM app.notifications WHERE user_id = $1::uuid ORDER BY id`, user)
	if err != nil {
		w.t.Fatal(err)
	}
	out, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		w.t.Fatal(err)
	}
	return out
}
