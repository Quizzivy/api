//go:build integration

package application_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"quizzivy/internal/modules/classes/application"
	"quizzivy/internal/modules/classes/application/command"
	"quizzivy/internal/modules/classes/domain"
	"quizzivy/internal/shared/access"
)

type teacherAction func(ctx context.Context, svc *application.Application, classID, teacherID string) error

func redeemWhileTheTeacherActs(t *testing.T, pool *pgxpool.Pool, act teacherAction) (classID string, joined domain.EnrolResult) {
	t.Helper()
	classID, teacherID, _ := makeClassRow(t, pool)
	code := issueCode(t, newSvc(t, pool), classID, teacherID)
	m := newMember(t)
	dropUser(t, pool, m.Email)
	teacherPool, teacherPid := ownConnection(t)
	studentPool, studentPid := ownConnection(t)
	release, gate := holding(t, pool, `
		INSERT INTO app.users (email, full_name, role_id)
		VALUES ($1, 'Giữ chỗ', (SELECT id FROM app.roles WHERE builtin_key = 'student'))`, m.Email)

	type enrolment struct {
		result domain.EnrolResult
		err    error
	}
	enrol := make(chan enrolment, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		result, err := withKeys(studentPool, joinKeys).Commands.EnrolNewMember.Handle(ctx, command.EnrolNewMember{Member: m, Code: code})
		enrol <- enrolment{result, err}
	}()
	waitUntilBlocked(t, pool, studentPid, gate, "the redemption, holding the class and the code row, on its new account's email")

	acted := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		acted <- act(ctx, withKeys(teacherPool, joinKeys), classID, teacherID)
	}()
	waitUntilBlocked(t, pool, teacherPid, studentPid, "the teacher, on the class row the redemption holds")
	release()

	redeemed := finished(t, "the redemption", enrol)
	if err := finished(t, "the teacher's action", acted); err != nil {
		t.Fatalf("the teacher's only attempt, which met a redemption holding the code row: %v", err)
	}
	if redeemed.err != nil || redeemed.result.Outcome != domain.PreviewOK {
		t.Fatalf("the redemption answered %+v (%v), want the student enrolled", redeemed.result, redeemed.err)
	}
	if n := memberCount(t, pool, classID); n != 2 {
		t.Errorf("the class has %d members, want the student it started with and the one who redeemed", n)
	}
	return classID, redeemed.result
}

func TestATeachersRotateOrRevokeAndARedemptionNeverDeadlock(t *testing.T) {
	pool := newPool(t)

	t.Run("rotate", func(t *testing.T) {
		classID, _ := redeemWhileTheTeacherActs(t, pool, func(ctx context.Context, svc *application.Application, classID, teacherID string) error {
			_, err := svc.Commands.Rotate.Handle(ctx, command.Rotate{Request: domain.RotateRequest{ClassID: classID, ActorUserID: teacherID}})
			return err
		})
		if n := activeCodeCount(t, pool, classID); n != 1 {
			t.Errorf("the class holds %d active codes after the rotation, want one", n)
		}
		if !selfJoinEnabled(t, pool, classID) {
			t.Error("the class is closed after a rotation")
		}
	})

	t.Run("revoke", func(t *testing.T) {
		classID, _ := redeemWhileTheTeacherActs(t, pool, func(ctx context.Context, svc *application.Application, classID, teacherID string) error {
			_, err := svc.Commands.Revoke.Handle(ctx, command.Revoke{Request: domain.RevokeRequest{ClassID: classID, ActorUserID: teacherID}})
			return err
		})
		if n := activeCodeCount(t, pool, classID); n != 0 {
			t.Errorf("the class holds %d active codes after the revocation, want none", n)
		}
		if selfJoinEnabled(t, pool, classID) {
			t.Error("the class is open after a revocation")
		}
	})
}

// TestAJoinTakesTheClassBeforeTheCodeAndTiesTheNewTeacher: a redemption locks
// the class FOR SHARE before it claims the code (T-R7.9), so it waits for a
// change of teacher in flight without holding the code row a Revoke would need,
// and the tie it records names the class's new teacher.
func TestAJoinTakesTheClassBeforeTheCodeAndTiesTheNewTeacher(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	classID, teacherID, _ := makeClassRow(t, pool)
	_, heir, _ := makeClassRow(t, pool)
	_, _, joiner := makeClassRow(t, pool)
	code := issueCode(t, newSvc(t, pool), classID, teacherID)
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM app.audit_log WHERE entity_id = $1::uuid`, classID)
		_, _ = pool.Exec(c, `DELETE FROM app.class_members WHERE class_id = $1`, classID)
		_, _ = pool.Exec(c, `DELETE FROM app.class_join_codes WHERE class_id = $1`, classID)
		_, _ = pool.Exec(c, `DELETE FROM app.classes WHERE id = $1`, classID)
	})
	studentPool, studentPid := ownConnection(t)
	change := newHeldTx(t, pool, `UPDATE app.classes SET teacher_id = $2 WHERE id = $1`, classID, heir)

	joined := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		result, err := withKeys(studentPool, joinKeys).Commands.EnrolExisting.Handle(ctx, command.EnrolExisting{UserID: joiner, Code: code})
		if err == nil && result.Outcome != domain.PreviewOK {
			err = fmt.Errorf("the join answered %+v", result)
		}
		joined <- err
	}()
	waitUntilBlocked(t, pool, studentPid, change.pid, "the join, on the class a change of teacher holds")

	probe, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := probe.Exec(ctx, `SELECT 1 FROM app.class_join_codes WHERE class_id = $1 AND revoked_at IS NULL FOR UPDATE NOWAIT`, classID); err != nil {
		t.Errorf("the waiting join holds the code row (%v): it claimed the code before the class, the order a Revoke deadlocks with", err)
	}
	_ = probe.Rollback(ctx)

	if err := change.commit(); err != nil {
		t.Fatalf("commit the change of teacher: %v", err)
	}
	if err := finished(t, "the join", joined); err != nil {
		t.Fatalf("the join after the change of teacher: %v", err)
	}
	var tied bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM app.messaging_ties WHERE staff_id = $1 AND student_id = $2)`, heir, joiner).Scan(&tied); err != nil {
		t.Fatal(err)
	}
	if !tied {
		t.Error("the class's new teacher is not tied to the student who joined while the change was in flight")
	}
}

func TestARotateInFlightStillHoldsBackEveryOtherWriterOfTheClass(t *testing.T) {
	pool := newPool(t)
	classID, teacherID, _ := makeClassRow(t, pool)
	issueCode(t, newSvc(t, pool), classID, teacherID)
	rotatePool, rotatePid := ownConnection(t)
	revokePool, revokePid := ownConnection(t)
	updatePool, updatePid := ownConnection(t)
	release, gate := holding(t, pool, `SELECT 1 FROM app.users WHERE id = $1 FOR UPDATE`, teacherID)
	renamed := "Lớp đổi tên"

	rotate := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, err := withKeys(rotatePool, joinKeys).Commands.Rotate.Handle(ctx, command.Rotate{Request: domain.RotateRequest{ClassID: classID, ActorUserID: teacherID}})
		rotate <- err
	}()
	waitUntilBlocked(t, pool, rotatePid, gate, "the teacher's Rotate, on its new row's creator")

	revoke := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, err := withKeys(revokePool, joinKeys).Commands.Revoke.Handle(ctx, command.Revoke{Request: domain.RevokeRequest{ClassID: classID, ActorUserID: teacherID}})
		revoke <- err
	}()
	waitUntilBlocked(t, pool, revokePid, rotatePid, "the teacher's Revoke, on the class row")

	update := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, err := withKeys(updatePool, joinKeys).Commands.Update.Handle(ctx, command.Update{
			ClassID: classID, Input: domain.UpdateInput{Name: &renamed}, Scope: access.Scope{UserID: teacherID}})
		update <- err
	}()
	waitUntilBlocked(t, pool, updatePid, revokePid, "a rename of the class, in line behind Revoke for the class row")

	release()

	for what, done := range map[string]chan error{"Rotate": rotate, "Revoke": revoke, "the rename": update} {
		if err := finished(t, what, done); err != nil {
			t.Errorf("%s: %v", what, err)
		}
	}
	if n := activeCodeCount(t, pool, classID); n != 0 {
		t.Errorf("the class holds %d active codes, want none: Revoke ran after Rotate", n)
	}
	if selfJoinEnabled(t, pool, classID) {
		t.Error("the class is open, although Revoke ran after Rotate")
	}
	var name string
	if err := pool.QueryRow(context.Background(), `SELECT name FROM app.classes WHERE id = $1`, classID).Scan(&name); err != nil || name != renamed {
		t.Errorf("the class is named %q (%v), want %q", name, err, renamed)
	}
}
