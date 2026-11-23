//go:build integration

package application_test

import (
	"context"
	"errors"
	"quizzivy/internal/modules/identity/application"
	"quizzivy/internal/modules/identity/application/command"
	"quizzivy/internal/modules/identity/domain"
	"quizzivy/internal/modules/identity/repositories"
	"quizzivy/internal/platform/db"
	"quizzivy/internal/shared/access"
	"testing"
	"time"
)

func TestStudentDeletionPreservesAuditActors(t *testing.T) {
	pool := newPool(t)
	w := seedWorld(t, pool, "10.00")
	svc := application.New(nil, nil, 0, repositories.NewStudents(db.NewContext(pool)), nil)
	ctx := context.Background()
	req := domain.WriteRequest{ActorID: w.admin}
	if _, err := svc.Commands.DeleteStudent.Handle(ctx, command.DeleteStudent{Request: req, ID: w.student}); !errors.Is(err, domain.ErrNotArchived) {
		t.Fatalf("active deletion = %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE app.users SET disabled_at = now() WHERE id = $1`, w.student); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO app.audit_log (actor_user_id, action, entity) VALUES ($1, 'login', 'user')`, w.student); err != nil {
		t.Fatal(err)
	}
	_, err := svc.Commands.DeleteStudent.Handle(ctx, command.DeleteStudent{Request: req, ID: w.student})
	var refused *domain.ReferencedError
	if !errors.Is(err, domain.ErrReferenced) || !errors.As(err, &refused) || refused.By != domain.ReferencedByAudit {
		t.Fatalf("audit actor deletion = %v, want referenced by audit", err)
	}
	var retained int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM app.audit_log WHERE actor_user_id = $1`, w.student).Scan(&retained); err != nil {
		t.Fatal(err)
	}
	if retained == 0 {
		t.Fatal("audit actor was rewritten or removed")
	}
}

func TestUnusedDisabledStudentCanBePermanentlyDeleted(t *testing.T) {
	pool := newPool(t)
	w := seedWorld(t, pool, "10.00")
	svc := application.New(nil, nil, 0, repositories.NewStudents(db.NewContext(pool)), nil)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `UPDATE app.users SET disabled_at = now() WHERE id = $1`, w.student); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Commands.DeleteStudent.Handle(ctx, command.DeleteStudent{Request: domain.WriteRequest{ActorID: w.admin}, ID: w.student}); err != nil {
		t.Fatal(err)
	}
	var exists bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM app.users WHERE id = $1)`, w.student).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Fatal("deleted student still exists")
	}
}

func TestARefusedDeletionNamesWhatHoldsTheStudent(t *testing.T) {
	w := newRosterWorld(t)
	ctx := context.Background()
	admin := domain.WriteRequest{ActorID: w.admin, All: true, Grants: access.NewSet(access.All()...)}
	owner := w.user(t, "student", "Chủ đề thi", nil)
	w.exec(t, `INSERT INTO app.tests (title, created_by, owner_id) VALUES ('Đề của học viên', $1, $2)`, w.admin, owner)
	sitter := w.user(t, "student", "Người làm bài", nil)
	w.enrol(t, w.classA, sitter)
	w.graded(t, w.assignment(t, w.a, &w.classA, nil), sitter, time.Hour)
	for student, want := range map[string]domain.Reference{
		owner:      domain.ReferencedByOwnedContent,
		sitter:     domain.ReferencedByAttempts,
		w.targeted: domain.ReferencedByAssignments,
	} {
		w.exec(t, `UPDATE app.users SET disabled_at = now() WHERE id = $1`, student)
		_, err := w.app.Commands.DeleteStudent.Handle(ctx, command.DeleteStudent{Request: admin, ID: student})
		var refused *domain.ReferencedError
		if !errors.As(err, &refused) || refused.By != want {
			t.Errorf("deleting a student held by %s answered %v", want, err)
		}
		if w.id(t, `SELECT count(*)::text FROM app.users WHERE id = $1`, student) != "1" {
			t.Errorf("the student held by %s was deleted", want)
		}
	}
}

func TestARefusedDeletionNamesTheSetsAndGrantsAUserHolds(t *testing.T) {
	w := newRosterWorld(t)
	ctx := context.Background()
	admin := domain.WriteRequest{ActorID: w.admin, All: true, Grants: access.NewSet(access.All()...)}
	question := w.id(t, `INSERT INTO app.questions (type, prompt, points, created_by, owner_id)
		VALUES ('short_answer', 'Câu trong bộ', 1.0, $1, $1) RETURNING id::text`, w.admin)
	adminSet := func(t *testing.T) string {
		t.Helper()
		return w.id(t, `INSERT INTO app.question_sets (owner_id, name, created_by) VALUES ($1, 'Bộ của quản trị', $1) RETURNING id::text`, w.admin)
	}
	holders := map[string]func(t *testing.T, user string){
		"owns a set": func(t *testing.T, user string) {
			w.exec(t, `INSERT INTO app.question_sets (owner_id, name, created_by) VALUES ($1, 'Bộ của học viên', $2)`, user, w.admin)
		},
		"made a set": func(t *testing.T, user string) {
			w.exec(t, `INSERT INTO app.question_sets (owner_id, name, created_by) VALUES ($1, 'Bộ được tạo hộ', $2)`, w.admin, user)
		},
		"added a set item": func(t *testing.T, user string) {
			w.exec(t, `INSERT INTO app.question_set_items (set_id, question_id, ordinal, added_by) VALUES ($1, $2, 0, $3)`, adminSet(t), question, user)
		},
		"granted a share": func(t *testing.T, user string) {
			w.exec(t, `INSERT INTO app.content_shares (kind, question_set_id, grantee_id, access, granted_by)
				VALUES ('question_set', $1, $2, 'use', $3)`, adminSet(t), w.admin, user)
		},
	}
	for name, hold := range holders {
		user := w.user(t, "student", "Người giữ "+name, nil)
		hold(t, user)
		w.exec(t, `UPDATE app.users SET disabled_at = now() WHERE id = $1`, user)
		_, err := w.app.Commands.DeleteStudent.Handle(ctx, command.DeleteStudent{Request: admin, ID: user})
		var refused *domain.ReferencedError
		if !errors.As(err, &refused) || refused.By != domain.ReferencedByOwnedContent {
			t.Errorf("deleting a user who %s answered %v, want owned_content", name, err)
		}
		if w.id(t, `SELECT count(*)::text FROM app.users WHERE id = $1`, user) != "1" {
			t.Errorf("the user who %s was deleted", name)
		}
	}
}

// TestARefusedDeletionNamesTheMessagesAUserHolds keeps T-R7.9's keys to
// app.users mapped: a student with a direct thread, one who sent a message,
// and one who uploaded a file each answer referencedBy messages, never a 500.
func TestARefusedDeletionNamesTheMessagesAUserHolds(t *testing.T) {
	w := newRosterWorld(t)
	ctx := context.Background()
	admin := domain.WriteRequest{ActorID: w.admin, All: true, Grants: access.NewSet(access.All()...)}
	thread := func(t *testing.T, student string) string {
		t.Helper()
		return w.id(t, `INSERT INTO app.conversations (kind, staff_id, student_id, last_message_at)
			VALUES ('direct', $1, $2, now()) RETURNING id::text`, w.a, student)
	}
	holders := map[string]func(t *testing.T, user string){
		"has a direct thread": func(t *testing.T, user string) {
			w.exec(t, `INSERT INTO app.messages (conversation_id, sender_id, client_id, body) VALUES ($1, $2, uuidv7(), 'Chào em')`, thread(t, user), w.a)
		},
		"sent a message": func(t *testing.T, user string) {
			other := w.user(t, "student", "Bạn cùng lớp", nil)
			w.exec(t, `INSERT INTO app.messages (conversation_id, sender_id, client_id, body) VALUES ($1, $2, uuidv7(), 'Em chào cô')`, thread(t, other), user)
		},
		"uploaded a file": func(t *testing.T, user string) {
			w.exec(t, `INSERT INTO app.message_attachments (uploaded_by, storage_key, mime_type, bytes, original_filename, checksum_sha256)
				VALUES ($1, 'messages/' || uuidv7() || '.pdf', 'application/pdf', 1024, 'bai-tap.pdf', sha256('x'))`, user)
		},
	}
	for name, hold := range holders {
		user := w.user(t, "student", "Người giữ "+name, nil)
		hold(t, user)
		w.exec(t, `UPDATE app.users SET disabled_at = now() WHERE id = $1`, user)
		_, err := w.app.Commands.DeleteStudent.Handle(ctx, command.DeleteStudent{Request: admin, ID: user})
		var refused *domain.ReferencedError
		if !errors.As(err, &refused) || refused.By != domain.ReferencedByMessages {
			t.Errorf("deleting a user who %s answered %v, want messages", name, err)
		}
		if w.id(t, `SELECT count(*)::text FROM app.users WHERE id = $1`, user) != "1" {
			t.Errorf("the user who %s was deleted", name)
		}
	}
}

// TestARefusedDeletionNamesTheColleagueThreadAUserHolds keeps T-F2.2's keys to
// app.users mapped: deleteUser reaches only a disabled student account, so the
// staff member here held a colleague thread and was then given the student
// role. On either side of the pair, the smaller id or the larger, the deletion
// answers referencedBy messages, a 409, never a 500, and the account stays.
// The thread has no message, so only the conversation's key refuses it.
func TestARefusedDeletionNamesTheColleagueThreadAUserHolds(t *testing.T) {
	w := newRosterWorld(t)
	ctx := context.Background()
	admin := domain.WriteRequest{ActorID: w.admin, All: true, Grants: access.NewSet(access.All()...)}
	for _, side := range []string{"low", "high"} {
		user := w.user(t, "teacher", "Giáo viên đổi vai "+side, nil)
		// uuidv7 orders by creation: w.a is older than user, a later colleague
		// newer.
		colleague := w.a
		if side == "low" {
			colleague = w.user(t, "teacher", "Đồng nghiệp mới", nil)
		}
		w.exec(t, `INSERT INTO app.conversations (kind, colleague_low_id, colleague_high_id, last_message_at)
			VALUES ('colleague', LEAST($1::uuid, $2::uuid), GREATEST($1::uuid, $2::uuid), now())`, user, colleague)
		if got := w.id(t, `SELECT CASE WHEN colleague_low_id = $1 THEN 'low' ELSE 'high' END FROM app.conversations
			WHERE kind = 'colleague' AND $1 IN (colleague_low_id, colleague_high_id)`, user); got != side {
			t.Fatalf("the user sits on the %s side, want %s", got, side)
		}
		w.exec(t, `UPDATE app.users SET role_id = (SELECT id FROM app.roles WHERE builtin_key = 'student'), disabled_at = now() WHERE id = $1`, user)
		_, err := w.app.Commands.DeleteStudent.Handle(ctx, command.DeleteStudent{Request: admin, ID: user})
		var refused *domain.ReferencedError
		if !errors.Is(err, domain.ErrReferenced) || !errors.As(err, &refused) || refused.By != domain.ReferencedByMessages {
			t.Errorf("deleting a user on the %s side of a colleague thread answered %v, want messages", side, err)
		}
		if w.id(t, `SELECT count(*)::text FROM app.users WHERE id = $1`, user) != "1" {
			t.Errorf("the user on the %s side was deleted", side)
		}
	}
}

// TestARefusedDeletionNamesTheAttendanceAndTimetableAUserHolds keeps T-R8.2's
// keys to app.users mapped: a student with a mark, and a user who took one,
// answer referencedBy attendance; a user who made a term, a weekly series or
// a session answers owned_content. Each is a 409, never a 500.
func TestARefusedDeletionNamesTheAttendanceAndTimetableAUserHolds(t *testing.T) {
	w := newRosterWorld(t)
	ctx := context.Background()
	admin := domain.WriteRequest{ActorID: w.admin, All: true, Grants: access.NewSet(access.All()...)}
	session := func(t *testing.T) string {
		t.Helper()
		return w.id(t, `INSERT INTO app.class_sessions (class_id, starts_at, duration_minutes, created_by) VALUES ($1, now(), 90, $2) RETURNING id::text`, w.classA, w.a)
	}
	for name, c := range map[string]struct {
		hold func(t *testing.T, user string)
		want domain.Reference
	}{
		"has a mark": {func(t *testing.T, user string) {
			w.enrol(t, w.classA, user)
			w.exec(t, `INSERT INTO app.attendance_marks (session_id, student_id, status, marked_by, marked_at) VALUES ($1, $2, 'absent', $3, now())`, session(t), user, w.a)
		}, domain.ReferencedByAttendance},
		"took a mark": {func(t *testing.T, user string) {
			w.exec(t, `INSERT INTO app.attendance_marks (session_id, student_id, status, marked_by, marked_at) VALUES ($1, $2, 'present', $3, now())`, session(t), w.member, user)
		}, domain.ReferencedByAttendance},
		"made a term": {func(t *testing.T, user string) {
			// A day of its own, far from any other test's term: terms never overlap.
			w.exec(t, `INSERT INTO app.terms (name, starts_on, ends_on, created_by)
				SELECT 'Học kỳ', d, d, $1 FROM (SELECT DATE '4000-01-01' + (random() * 1000000)::int AS d) AS day`, user)
		}, domain.ReferencedByOwnedContent},
		"made a series": {func(t *testing.T, user string) {
			w.exec(t, `INSERT INTO app.class_session_series (class_id, weekday, start_time, duration_minutes, starts_on, time_zone, created_by)
				VALUES ($1, 2, '18:00', 90, DATE '2026-09-08', 'Asia/Ho_Chi_Minh', $2)`, w.classA, user)
		}, domain.ReferencedByOwnedContent},
		"made a session": {func(t *testing.T, user string) {
			w.exec(t, `INSERT INTO app.class_sessions (class_id, starts_at, duration_minutes, created_by) VALUES ($1, now(), 90, $2)`, w.classA, user)
		}, domain.ReferencedByOwnedContent},
	} {
		user := w.user(t, "student", "Người giữ "+name, nil)
		c.hold(t, user)
		w.exec(t, `UPDATE app.users SET disabled_at = now() WHERE id = $1`, user)
		_, err := w.app.Commands.DeleteStudent.Handle(ctx, command.DeleteStudent{Request: admin, ID: user})
		var refused *domain.ReferencedError
		if !errors.Is(err, domain.ErrReferenced) || !errors.As(err, &refused) || refused.By != c.want {
			t.Errorf("deleting a user who %s answered %v, want %s", name, err, c.want)
		}
		if w.id(t, `SELECT count(*)::text FROM app.users WHERE id = $1`, user) != "1" {
			t.Errorf("the user who %s was deleted", name)
		}
	}
}
