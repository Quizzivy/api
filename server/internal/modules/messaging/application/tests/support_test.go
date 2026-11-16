//go:build integration

package application_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"quizzivy/internal/modules/messaging/application"
	"quizzivy/internal/modules/messaging/application/command"
	"quizzivy/internal/modules/messaging/application/query"
	"quizzivy/internal/modules/messaging/domain"
	messagingrepo "quizzivy/internal/modules/messaging/repositories"
	"quizzivy/internal/platform/db"
	"quizzivy/internal/shared/access"
	"quizzivy/internal/shared/actor"
	"quizzivy/internal/shared/ties"
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
	app := application.New(messagingrepo.NewPostgres(db.NewContext(tx))).WithAvatars(photos{})
	return &world{t: t, tx: tx, app: app, marker: "zm" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")}
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

func (w *world) value(sql string, dest any, args ...any) {
	w.t.Helper()
	if err := w.tx.QueryRow(context.Background(), sql, args...).Scan(dest); err != nil {
		w.t.Fatalf("%s: %v", sql, err)
	}
}

// user makes an account of the built-in role, created by creator when one is
// named.
func (w *world) user(name, builtin string, creator ...string) string {
	w.t.Helper()
	var by *string
	if len(creator) > 0 {
		by = &creator[0]
	}
	return w.id(`INSERT INTO app.users (email, full_name, role_id, avatar_key, created_by)
		VALUES ($1, $2, (SELECT id FROM app.roles WHERE builtin_key = $3), 'avatars/' || $1, $4::uuid) RETURNING id::text`,
		w.marker+"-"+uuid.NewString()[:8]+"@example.test", name+" "+w.marker, builtin, by)
}

func (w *world) class(teacher, name string) string {
	w.t.Helper()
	return w.id(`INSERT INTO app.classes (name, teacher_id) VALUES ($1, $2::uuid) RETURNING id::text`, name+" "+w.marker, teacher)
}

// enrol puts student in class as staff do, recording the tie as the classes
// module does.
func (w *world) enrol(class, student string) {
	w.t.Helper()
	w.exec(`INSERT INTO app.class_members (class_id, user_id, joined_via) VALUES ($1::uuid, $2::uuid, 'admin')`, class, student)
	if err := ties.Enrolled(context.Background(), w.tx, class, student); err != nil {
		w.t.Fatal(err)
	}
}

func (w *world) remove(class, student string) {
	w.t.Helper()
	w.exec(`DELETE FROM app.class_members WHERE class_id = $1::uuid AND user_id = $2::uuid`, class, student)
}

func (w *world) disable(user string) {
	w.t.Helper()
	w.exec(`UPDATE app.users SET disabled_at = now() WHERE id = $1::uuid`, user)
}

func (w *world) archive(class string) {
	w.t.Helper()
	w.exec(`UPDATE app.classes SET archived_at = now() WHERE id = $1::uuid`, class)
}

func by(id string) actor.Actor {
	return actor.Actor{ID: id, IP: "203.0.113.9", UserAgent: "messaging-test", Scope: access.Scope{UserID: id}}
}

func byAdmin(id string) actor.Actor {
	a := by(id)
	a.Scope.All = true
	return a
}

func text(s string) *string { return &s }

// direct posts body from who to student with a new clientId.
func (w *world) direct(who actor.Actor, student, body string) (domain.Posted, error) {
	w.t.Helper()
	return w.app.Commands.PostDirect.Handle(context.Background(), command.PostDirect{
		Actor: who, StudentID: student, ClientID: uuid.NewString(), Body: text(body),
	})
}

func (w *world) mustDirect(who actor.Actor, student, body string) domain.Message {
	w.t.Helper()
	posted, err := w.direct(who, student, body)
	if err != nil {
		w.t.Fatalf("%s writing to %s: %v", who.ID, student, err)
	}
	return posted.Message
}

func (w *world) announce(who actor.Actor, class, body string) (domain.Posted, error) {
	w.t.Helper()
	return w.app.Commands.PostAnnouncement.Handle(context.Background(), command.PostAnnouncement{
		Actor: who, MayWrite: true, ClassID: class, ClientID: uuid.NewString(), Body: text(body),
	})
}

func (w *world) mustAnnounce(who actor.Actor, class, body string) domain.Message {
	w.t.Helper()
	posted, err := w.announce(who, class, body)
	if err != nil {
		w.t.Fatalf("%s announcing to %s: %v", who.ID, class, err)
	}
	return posted.Message
}

// studentWrites is the student's post in their thread with staff, through
// the student side (T-R7.10b).
func (w *world) studentWrites(staff, student, body string) string {
	w.t.Helper()
	return w.mustAsStudent(student, staff, body).ID
}

// asStudent posts body from student to teacher with a new clientId.
func (w *world) asStudent(student, teacher, body string) (domain.StudentPosted, error) {
	w.t.Helper()
	return w.app.Commands.PostAsStudent.Handle(context.Background(), command.PostAsStudent{
		StudentID: student, TeacherID: teacher, ClientID: uuid.NewString(), Body: body,
	})
}

func (w *world) mustAsStudent(student, teacher, body string) domain.StudentMessage {
	w.t.Helper()
	posted, err := w.asStudent(student, teacher, body)
	if err != nil {
		w.t.Fatalf("%s writing to %s: %v", student, teacher, err)
	}
	return posted.Message
}

func (w *world) studentThread(student, teacher string, cursor domain.Cursor) (query.StudentDirect, error) {
	w.t.Helper()
	return w.app.Queries.StudentDirectThread.Handle(context.Background(), query.StudentDirectThread{StudentID: student, TeacherID: teacher, Cursor: cursor})
}

func (w *world) studentChannel(student, class string, cursor domain.Cursor) (query.StudentClass, error) {
	w.t.Helper()
	return w.app.Queries.StudentClassThread.Handle(context.Background(), query.StudentClassThread{StudentID: student, ClassID: class, Cursor: cursor})
}

func (w *world) studentList(student string) []domain.StudentThread {
	w.t.Helper()
	threads, err := w.app.Queries.StudentThreads.Handle(context.Background(), query.StudentThreads{StudentID: student})
	if err != nil {
		w.t.Fatal(err)
	}
	return threads
}

func (w *world) studentRead(student string, mark command.MarkReadAsStudent) error {
	w.t.Helper()
	mark.StudentID = student
	_, err := w.app.Commands.MarkReadAsStudent.Handle(context.Background(), mark)
	return err
}

func (w *world) thread(staff, student string, cursor domain.Cursor) (query.DirectThread, error) {
	w.t.Helper()
	return w.app.Queries.StaffDirectThread.Handle(context.Background(), query.StaffDirectThread{ActorID: staff, StudentID: student, Cursor: cursor})
}

func (w *world) channel(who actor.Actor, class string, cursor domain.Cursor) (query.ClassThread, error) {
	w.t.Helper()
	return w.app.Queries.StaffClassThread.Handle(context.Background(), query.StaffClassThread{ReaderID: who.ID, Scope: who.Scope, ClassID: class, Cursor: cursor})
}

func (w *world) list(who actor.Actor, tab domain.Tab, q string) []domain.StaffThread {
	w.t.Helper()
	threads, err := w.app.Queries.StaffThreads.Handle(context.Background(), query.StaffThreads{ActorID: who.ID, All: who.Scope.All, Tab: tab, Query: q})
	if err != nil {
		w.t.Fatal(err)
	}
	return threads
}

func (w *world) read(who actor.Actor, ref domain.ThreadRef, message string) error {
	w.t.Helper()
	_, err := w.app.Commands.MarkRead.Handle(context.Background(), command.MarkRead{Actor: who, Thread: ref, MessageID: message})
	return err
}

// mine keeps the rows of the list this test made, so rows other tests left in
// the database do not count.
func (w *world) mine(threads []domain.StaffThread) []domain.StaffThread {
	var out []domain.StaffThread
	for _, t := range threads {
		if strings.Contains(t.Title, w.marker) {
			out = append(out, t)
		}
	}
	return out
}

func wantErr(t *testing.T, what string, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Errorf("%s: %v, want %v", what, err, want)
	}
}

// queryStaffDirect is who's read of their thread with student, in who's
// scope.
func queryStaffDirect(who actor.Actor, student string) query.StaffDirectThread {
	return query.StaffDirectThread{ActorID: who.ID, Scope: who.Scope, StudentID: student}
}
