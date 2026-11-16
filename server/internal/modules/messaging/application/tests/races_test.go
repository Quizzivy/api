//go:build integration

package application_test

import (
	"context"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"quizzivy/internal/modules/messaging/application"
	"quizzivy/internal/modules/messaging/application/command"
	"quizzivy/internal/modules/messaging/application/query"
	"quizzivy/internal/modules/messaging/domain"
	messagingrepo "quizzivy/internal/modules/messaging/repositories"
	"quizzivy/internal/platform/db"
	"quizzivy/internal/shared/actor"
)

// pooled is a world whose rows are committed, so separate connections can
// contend for them: the shared transaction of newWorld turns every post into
// a savepoint of one connection, where nothing ever waits. Its cleanup
// removes what it made.
type pooled struct {
	t       *testing.T
	pool    *pgxpool.Pool
	app     *application.Application
	marker  string
	users   []string
	classes []string
}

func newPooled(t *testing.T) *pooled {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	p := &pooled{t: t, pool: pool, marker: "zr" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")}
	p.app = appOver(db.NewContext(pool))
	t.Cleanup(func() {
		p.cleanUp()
		pool.Close()
	})
	return p
}

func appOver(dbx db.Context) *application.Application {
	return application.New(messagingrepo.NewPostgres(dbx)).WithAvatars(photos{})
}

// cleanUp deletes the world's threads, then its classes, then its accounts,
// newest first so an account goes before the one that created it. The app
// role may not delete messages, so under it the rows stay, marked.
func (p *pooled) cleanUp() {
	ctx := context.Background()
	threads := `SELECT v.id FROM app.conversations v WHERE v.staff_id = ANY($1::uuid[]) OR v.class_id = ANY($2::uuid[])`
	for _, sql := range []string{
		`DELETE FROM app.conversation_reads WHERE conversation_id IN (` + threads + `)`,
		`DELETE FROM app.messages WHERE conversation_id IN (` + threads + `)`,
		`DELETE FROM app.conversations WHERE id IN (` + threads + `)`,
	} {
		if _, err := p.pool.Exec(ctx, sql, p.users, p.classes); err != nil {
			p.t.Logf("cleanup: %v", err)
			return
		}
	}
	for sql, ids := range map[string][]string{
		`DELETE FROM app.messaging_ties WHERE staff_id = ANY($1::uuid[]) OR student_id = ANY($1::uuid[])`: p.users,
		`DELETE FROM app.class_members WHERE class_id = ANY($1::uuid[])`:                                  p.classes,
	} {
		if _, err := p.pool.Exec(ctx, sql, ids); err != nil {
			p.t.Logf("cleanup: %v", err)
		}
	}
	if _, err := p.pool.Exec(ctx, `DELETE FROM app.classes WHERE id = ANY($1::uuid[])`, p.classes); err != nil {
		p.t.Logf("cleanup: %v", err)
	}
	for _, id := range slices.Backward(p.users) {
		if _, err := p.pool.Exec(ctx, `DELETE FROM app.users WHERE id = $1::uuid`, id); err != nil {
			p.t.Logf("cleanup: %v", err)
		}
	}
}

func (p *pooled) id(sql string, args ...any) string {
	p.t.Helper()
	var id string
	if err := p.pool.QueryRow(context.Background(), sql, args...).Scan(&id); err != nil {
		p.t.Fatalf("%s: %v", sql, err)
	}
	return id
}

func (p *pooled) count(sql string, args ...any) int {
	p.t.Helper()
	var n int
	if err := p.pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		p.t.Fatalf("%s: %v", sql, err)
	}
	return n
}

func (p *pooled) user(name, builtin string, creator ...string) string {
	p.t.Helper()
	var by *string
	if len(creator) > 0 {
		by = &creator[0]
	}
	id := p.id(`INSERT INTO app.users (email, full_name, role_id, created_by)
		VALUES ($1, $2, (SELECT id FROM app.roles WHERE builtin_key = $3), $4::uuid) RETURNING id::text`,
		p.marker+"-"+uuid.NewString()[:8]+"@example.test", name+" "+p.marker, builtin, by)
	p.users = append(p.users, id)
	return id
}

func (p *pooled) class(teacher, name string) string {
	p.t.Helper()
	id := p.id(`INSERT INTO app.classes (name, teacher_id) VALUES ($1, $2::uuid) RETURNING id::text`, name+" "+p.marker, teacher)
	p.classes = append(p.classes, id)
	return id
}

// poster sends one post with clientID through app.
type poster func(app *application.Application, clientID, body string) (domain.Posted, error)

func directBy(who actor.Actor, student string) poster {
	return func(app *application.Application, clientID, body string) (domain.Posted, error) {
		return app.Commands.PostDirect.Handle(context.Background(), command.PostDirect{
			Actor: who, StudentID: student, ClientID: clientID, Body: text(body),
		})
	}
}

func announcementBy(who actor.Actor, class string) poster {
	return func(app *application.Application, clientID, body string) (domain.Posted, error) {
		return app.Commands.PostAnnouncement.Handle(context.Background(), command.PostAnnouncement{
			Actor: who, MayWrite: true, ClassID: class, ClientID: clientID, Body: text(body),
		})
	}
}

type result struct {
	posted domain.Posted
	err    error
}

// TestAThreadsPostsTakeTheirIdsInTheOrderTheyCommit: a post may make its
// message id only once the thread's earlier writers have committed. An id made
// before, committed after, would sort behind a message a reader has already
// polled past (after=latestId) or marked read, and that reader would never get
// it. First a real post is held open while a second waits; then the thread's
// row lock alone is held, the lock every post holds from before its id is made
// until it commits, so the waiting post would make its id at once if it took
// the lock only afterwards, as the last_message_at UPDATE does. In both, an id
// taken while the second post waits proves it: the second post's id must be
// greater.
func TestAThreadsPostsTakeTheirIdsInTheOrderTheyCommit(t *testing.T) {
	p := newPooled(t)
	teacher := p.user("Cô Ngọc", "teacher")
	admin := p.user("Quản trị", "admin")
	student := p.user("Học viên Khoa", "student", teacher)
	class := p.class(teacher, "Lớp 8A")
	ngoc, boss := by(teacher), byAdmin(admin)

	for name, c := range map[string]struct {
		first, held, waiting poster
		read                 func(after string) ([]domain.Message, error)
	}{
		"a direct thread, two tabs of the teacher": {
			first: directBy(ngoc, student), held: directBy(ngoc, student), waiting: directBy(ngoc, student),
			read: func(after string) ([]domain.Message, error) {
				th, err := p.app.Queries.StaffDirectThread.Handle(context.Background(), query.StaffDirectThread{ActorID: teacher, StudentID: student, Cursor: domain.Cursor{After: after}})
				return th.Page.Messages, err
			},
		},
		"a class channel, the teacher and an admin": {
			first: announcementBy(ngoc, class), held: announcementBy(ngoc, class), waiting: announcementBy(boss, class),
			read: func(after string) ([]domain.Message, error) {
				th, err := p.app.Queries.StaffClassThread.Handle(context.Background(), query.StaffClassThread{ReaderID: ngoc.ID, Scope: ngoc.Scope, ClassID: class, Cursor: domain.Cursor{After: after}})
				return th.Page.Messages, err
			},
		},
	} {
		first, err := c.first(p.app, uuid.NewString(), "Chào cả lớp.")
		if err != nil {
			t.Fatalf("%s: the first post: %v", name, err)
		}

		tx, err := p.pool.Begin(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		held, err := c.held(appOver(db.NewContext(tx)), uuid.NewString(), "Bài tập về nhà: trang 12.")
		if err != nil {
			_ = tx.Rollback(context.Background())
			t.Fatalf("%s: the held post: %v", name, err)
		}
		waiting := p.waitBehind(name+", behind a post", tx, c.waiting)
		if got := waiting.Message.ID; got <= held.Message.ID {
			t.Errorf("%s: the second post's id %s sorts before the held post's %s, which committed first", name, got, held.Message.ID)
		}
		polled, err := c.read(held.Message.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(polled) != 1 || polled[0].ID != waiting.Message.ID {
			t.Errorf("%s: a poll after the held post's id answers %v, want the second post", name, polled)
		}

		writer, err := p.pool.Begin(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Exec(context.Background(), `SELECT 1 FROM app.conversations WHERE id = $1::uuid FOR NO KEY UPDATE`, first.Message.ConversationID); err != nil {
			_ = writer.Rollback(context.Background())
			t.Fatal(err)
		}
		p.waitBehind(name+", behind the thread's lock", writer, c.waiting)
	}
}

// waitBehind sends post while tx is open, checks that it waits, takes an id
// meanwhile, commits tx, and checks that the post's id is greater than that
// one: it was made only once tx was gone.
func (p *pooled) waitBehind(name string, tx pgx.Tx, post poster) domain.Posted {
	p.t.Helper()
	done := make(chan result, 1)
	go func() {
		posted, err := post(p.app, uuid.NewString(), "Nhớ mang sách nhé.")
		done <- result{posted, err}
	}()
	select {
	case r := <-done:
		_ = tx.Rollback(context.Background())
		p.t.Fatalf("%s: the post committed (%v) while the thread's writer was open: the thread was not held", name, r.err)
	case <-time.After(300 * time.Millisecond):
	}
	meanwhile := p.id(`SELECT uuidv7()::text`)
	if err := tx.Commit(context.Background()); err != nil {
		p.t.Fatal(err)
	}
	r := <-done
	if r.err != nil {
		p.t.Fatalf("%s: the post: %v", name, r.err)
	}
	if got := r.posted.Message.ID; got <= meanwhile {
		p.t.Errorf("%s: the post's id %s was made while the thread's writer was open (an id made then: %s): "+
			"it could commit after a message whose id is greater", name, got, meanwhile)
	}
	return r.posted
}

// TestConcurrentFirstPostsMakeOneThread sends two first posts to a student
// with no thread at once, on separate connections. With one clientId, one
// post makes the message and the other is its replay (201 and 200, one id);
// with two, both are made. Either way there is one thread.
func TestConcurrentFirstPostsMakeOneThread(t *testing.T) {
	p := newPooled(t)
	teacher := p.user("Thầy Phúc", "teacher")
	phuc := by(teacher)

	for attempt := range 6 {
		sameClientID := attempt%2 == 0
		student := p.user("Học viên", "student", teacher)
		post := directBy(phuc, student)
		ids := [2]string{uuid.NewString(), uuid.NewString()}
		if sameClientID {
			ids[1] = ids[0]
		}

		var results [2]result
		var wg sync.WaitGroup
		start := make(chan struct{})
		for i := range 2 {
			wg.Go(func() {
				<-start
				posted, err := post(p.app, ids[i], "Chào em, thầy là giáo viên chủ nhiệm.")
				results[i] = result{posted, err}
			})
		}
		close(start)
		wg.Wait()

		for i, r := range results {
			if r.err != nil {
				t.Fatalf("attempt %d (same clientId %v): post %d: %v", attempt, sameClientID, i, r.err)
			}
		}
		threads := p.count(`SELECT count(*)::int FROM app.conversations WHERE kind = 'direct' AND staff_id = $1::uuid AND student_id = $2::uuid`, teacher, student)
		messages := p.count(`SELECT count(*)::int FROM app.messages m JOIN app.conversations v ON v.id = m.conversation_id
			WHERE v.kind = 'direct' AND v.staff_id = $1::uuid AND v.student_id = $2::uuid`, teacher, student)
		if threads != 1 {
			t.Errorf("attempt %d (same clientId %v): %d threads, want 1", attempt, sameClientID, threads)
		}
		a, b := results[0].posted, results[1].posted
		if sameClientID {
			if a.Created == b.Created || a.Message.ID != b.Message.ID || messages != 1 {
				t.Errorf("attempt %d, one clientId: created %v and %v, ids %s and %s, %d messages; want one made, one replay of it, one message",
					attempt, a.Created, b.Created, a.Message.ID, b.Message.ID, messages)
			}
		} else if !a.Created || !b.Created || a.Message.ID == b.Message.ID || messages != 2 {
			t.Errorf("attempt %d, two clientIds: created %v and %v, ids %s and %s, %d messages; want two made",
				attempt, a.Created, b.Created, a.Message.ID, b.Message.ID, messages)
		}
		if a.Message.ConversationID != b.Message.ConversationID {
			t.Errorf("attempt %d: the posts went to threads %s and %s", attempt, a.Message.ConversationID, b.Message.ConversationID)
		}
	}
}

// TestARemovalDuringAStudentsFirstPostLeavesNoThread: a student may write
// first only to the teacher of a class they are in, so a first post and the
// student's removal from that class must not interleave. The removal is held
// open while the student posts: the post waits on the membership it holds FOR
// SHARE, and once the removal commits it finds the student in no class of the
// teacher's and answers as for a teacher it may not name, making no thread.
// Without the hold, the post reads the membership the removal has not yet
// committed away and starts a thread with a teacher the student has left.
func TestARemovalDuringAStudentsFirstPostLeavesNoThread(t *testing.T) {
	p := newPooled(t)
	ctx := context.Background()
	teacher := p.user("Cô Diệp", "teacher")
	class := p.class(teacher, "Lớp 3A")
	student := p.user("Học viên Quân", "student")
	if _, err := p.pool.Exec(ctx, `INSERT INTO app.class_members (class_id, user_id, joined_via) VALUES ($1::uuid, $2::uuid, 'admin')`, class, student); err != nil {
		t.Fatal(err)
	}

	removal, err := p.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = removal.Rollback(ctx) }()
	if _, err := removal.Exec(ctx, `DELETE FROM app.class_members WHERE class_id = $1::uuid AND user_id = $2::uuid`, class, student); err != nil {
		t.Fatal(err)
	}
	done := make(chan result, 1)
	go func() {
		posted, err := p.app.Commands.PostAsStudent.Handle(ctx, command.PostAsStudent{
			StudentID: student, TeacherID: teacher, ClientID: uuid.NewString(), Body: "Em chào cô.",
		})
		done <- result{domain.Posted{Created: posted.Created}, err}
	}()
	select {
	case r := <-done:
		t.Fatalf("the first post finished while the removal was open: created %v, %v; want it to wait on the membership", r.posted.Created, r.err)
	case <-time.After(400 * time.Millisecond):
	}
	if err := removal.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	r := <-done
	wantErr(t, "the first post once the removal committed", r.err, domain.ErrNotFound)
	if n := p.count(`SELECT count(*)::int FROM app.conversations WHERE kind = 'direct' AND staff_id = $1::uuid AND student_id = $2::uuid`, teacher, student); n != 0 {
		t.Errorf("%d threads with the teacher the student left, want none", n)
	}
}
