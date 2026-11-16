//go:build integration

package application_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	questionscommand "quizzivy/internal/modules/questions/application/command"
	"quizzivy/internal/platform/db"
	"quizzivy/internal/shared/access"
	"testing"

	mediarepo "quizzivy/internal/modules/media/repositories"
	"quizzivy/internal/modules/tests/application"
	"quizzivy/internal/modules/tests/domain"
	"quizzivy/internal/modules/tests/repositories"

	"github.com/jackc/pgx/v5/pgxpool"

	questionsapp "quizzivy/internal/modules/questions/application"
	questionsdomain "quizzivy/internal/modules/questions/domain"
	questionsrepo "quizzivy/internal/modules/questions/repositories"
)

func newPool(t *testing.T) *pgxpool.Pool {
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
	return pool
}

func makeAuthor(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	nonce := make([]byte, 8)
	if _, err := rand.Read(nonce); err != nil {
		t.Fatal(err)
	}
	email := "tests-author-" + hex.EncodeToString(nonce) + "@example.com"

	var id string
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO app.users (email, full_name, role_id) VALUES ($1,'Giáo viên',(SELECT id FROM app.roles WHERE builtin_key = 'admin')) RETURNING id::text`,
		email).Scan(&id); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { removeAuthor(t, pool, id) })
	return id
}

// removeAuthor deletes what an author made and then the author, reporting a
// row it could not remove. The author is an Admin: on a fresh database the
// users_last_admin guard keeps the only active one, which is logged, not a
// failure.
func removeAuthor(t *testing.T, pool *pgxpool.Pool, id string) {
	t.Helper()
	c := context.Background()
	for _, sql := range []string{
		`DELETE FROM app.audit_log WHERE actor_user_id = $1`,
		`DELETE FROM app.test_versions WHERE published_by = $1`,
		`DELETE FROM app.tests WHERE created_by = $1`,
		`DELETE FROM app.questions WHERE created_by = $1`,
		// Media go after the questions and versions that name them.
		`DELETE FROM app.media_assets WHERE uploaded_by = $1`,
	} {
		if _, err := pool.Exec(c, sql, id); err != nil {
			t.Errorf("clean up %q: %v", sql, err)
		}
	}
	_, err := pool.Exec(c, `DELETE FROM app.users WHERE id = $1`, id)
	switch {
	case db.IsCheckViolation(err, "users_last_admin"):
		t.Logf("kept the Admin %s: it is the only active Admin, which the users_last_admin guard never lets go", id)
	case err != nil:
		t.Errorf("clean up: %v", err)
	}
}

func newService(t *testing.T, pool *pgxpool.Pool) *application.Application {
	t.Helper()
	return application.New(repositories.NewPostgres(db.NewContext(pool), questionsrepo.NewPostgres(db.NewContext(pool)), mediarepo.NewPostgres(db.NewContext(pool))))
}

func req(author string) domain.Request {
	return domain.Request{ActorID: author, Scope: access.Scope{UserID: author}}
}

func reqFor(id, author string) domain.Request {
	return domain.Request{ID: id, ActorID: author, Scope: access.Scope{UserID: author}}
}

// newQuestion adds a bank question the outline can reference.
func newQuestion(t *testing.T, pool *pgxpool.Pool, author, prompt string) string {
	t.Helper()
	svc := questionsapp.New(questionsrepo.NewPostgres(db.NewContext(pool)), mediaKinds{pool})
	q, err := svc.Commands.Create.Handle(context.Background(), questionscommand.Create{Request: questionsdomain.WriteRequest{
		Input: questionsdomain.Input{
			Type: questionsdomain.ShortAnswer, Prompt: prompt, Points: "2.00", Tags: []string{},
		},
		ActorID: author,
	}})
	if err != nil {
		t.Fatalf("question %q: %v", prompt, err)
	}
	return q.ID
}

var everyone = access.Scope{All: true}

var bothKeys = access.NewSet(access.ContentQuestionsWrite, access.ContentTestsWrite)
