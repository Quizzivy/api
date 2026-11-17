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
