//go:build integration

package application_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"quizzivy/internal/modules/classes/application/command"
	"quizzivy/internal/modules/classes/domain"
	"quizzivy/internal/modules/classes/repositories"
	"quizzivy/internal/platform/db"
	"quizzivy/internal/shared/ties"
)

// tied reports whether app.messaging_ties knows that staff has taught student
// (T-R7.9), and since when.
func (w *classWorld) tied(t *testing.T, staff, student string) (bool, time.Time) {
	t.Helper()
	var since time.Time
	err := w.tx.QueryRow(context.Background(), `SELECT since FROM app.messaging_ties WHERE staff_id = $1 AND student_id = $2`,
		staff, student).Scan(&since)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, time.Time{}
	}
	if err != nil {
		t.Fatal(err)
	}
	return true, since
}

func TestJoiningAClassTiesTheStudentToItsTeacherForGood(t *testing.T) {
	w := newClassWorld(t)
	ctx := context.Background()
	added := w.user(t, "student", "Học viên được thêm", &w.a)
	if _, err := w.svc.Commands.AddMember.Handle(ctx, command.AddMember{ClassID: w.classA, UserID: added, Actor: w.who(w.a)}); err != nil {
		t.Fatal(err)
	}
	if ok, _ := w.tied(t, w.a, added); !ok {
		t.Fatal("a student staff added is not tied to the class's teacher")
	}
	// The transaction's now() is one instant, so the first tie is moved back to
	// tell a second write from it.
	w.exec(t, `UPDATE app.messaging_ties SET since = since - interval '1 year' WHERE staff_id = $1 AND student_id = $2`, w.a, added)
	_, first := w.tied(t, w.a, added)

	joiner := w.user(t, "student", "Học viên tự vào", nil)
	code := issueCode(t, w.svc, w.classA, w.a)
	if _, err := w.svc.Commands.EnrolExisting.Handle(ctx, command.EnrolExisting{UserID: joiner, Code: code, Meta: domain.Meta{}}); err != nil {
		t.Fatal(err)
	}
	if ok, _ := w.tied(t, w.a, joiner); !ok {
		t.Fatal("a student who joined with a code is not tied to the class's teacher")
	}
	if ok, _ := w.tied(t, w.b, joiner); ok {
		t.Error("a teacher of no class of the student's is tied to them")
	}

	for _, student := range []string{added, joiner} {
		if _, err := w.svc.Commands.RemoveMember.Handle(ctx, command.RemoveMember{ClassID: w.classA, UserID: student, Actor: w.who(w.a)}); err != nil {
			t.Fatal(err)
		}
		if ok, _ := w.tied(t, w.a, student); !ok {
			t.Errorf("leaving the class removed the tie of %s", student)
		}
	}
	if _, err := w.svc.Commands.AddMember.Handle(ctx, command.AddMember{ClassID: w.classA, UserID: added, Actor: w.who(w.a)}); err != nil {
		t.Fatal(err)
	}
	if _, since := w.tied(t, w.a, added); !since.Equal(first) {
		t.Errorf("joining again moved since from %v to %v", first, since)
	}
}

// TestTheModuleAloneTiesAnEnrolledStudent holds what F2's migration (T-F2.2)
// assumes of R8's 00179: no trigger on the class tables writes a tie any more,
// so an enrolment through the module is tied by shared/ties alone, inside the
// enrolment's own transaction.
func TestTheModuleAloneTiesAnEnrolledStudent(t *testing.T) {
	w := newClassWorld(t)
	ctx := context.Background()
	var fills int
	if err := w.tx.QueryRow(ctx, `SELECT count(*) FROM pg_trigger t JOIN pg_proc p ON p.oid = t.tgfoid
		WHERE t.tgrelid IN ('app.class_members'::regclass, 'app.classes'::regclass) AND NOT t.tgisinternal
		  AND p.prosrc ILIKE '%messaging_ties%'`).Scan(&fills); err != nil {
		t.Fatal(err)
	}
	if fills != 0 {
		t.Fatalf("%d triggers on the class tables still write ties; 00179 drops them", fills)
	}
	student := w.user(t, "student", "Học viên ghi danh", &w.a)
	if _, err := w.svc.Commands.AddMember.Handle(ctx, command.AddMember{ClassID: w.classA, UserID: student, Actor: w.who(w.a)}); err != nil {
		t.Fatal(err)
	}
	w.exec(t, `SET CONSTRAINTS ALL IMMEDIATE`)
	if ok, _ := w.tied(t, w.a, student); !ok {
		t.Error("a student enrolled through the module is not tied to the class's teacher")
	}
}

func TestAClassGivenToAnotherTeacherTiesThemToEveryMember(t *testing.T) {
	w := newAdminWorld(t)
	ctx := context.Background()
	left := w.user(t, "student", "Học viên đã rời", &w.a)
	w.exec(t, `INSERT INTO app.messaging_ties (staff_id, student_id) VALUES ($1, $2)`, w.a, left)
	if ok, _ := w.tied(t, w.b, w.studentA); ok {
		t.Fatal("B is tied to A's student before the change")
	}
	if _, err := w.svc.Commands.ChangeTeacher.Handle(ctx, command.ChangeTeacher{ClassID: w.classA, TeacherID: w.b, Actor: w.who(w.admin)}); err != nil {
		t.Fatal(err)
	}
	if ok, _ := w.tied(t, w.b, w.studentA); !ok {
		t.Error("the class's new teacher is not tied to its member")
	}
	if ok, _ := w.tied(t, w.b, left); ok {
		t.Error("the class's new teacher is tied to a student who left before the change")
	}

	other := w.class(t, w.b)
	member := w.user(t, "student", "Học viên lớp B", &w.b)
	w.exec(t, `INSERT INTO app.class_members (class_id, user_id, joined_via, added_by) VALUES ($1, $2, 'admin', $3)`, other, member, w.b)
	moved, err := repositories.NewPostgres(db.NewContext(w.tx)).TransferOwnership(ctx, w.b, w.a)
	if err != nil || moved != 3 {
		t.Fatalf("transfer moved %d classes (%v), want B's three", moved, err)
	}
	for _, student := range []string{w.studentA, member} {
		if ok, _ := w.tied(t, w.a, student); !ok {
			t.Errorf("the transfer's new teacher is not tied to %s", student)
		}
	}
}

// TestAnAnonymisedMemberIsNotTiedAgain fails when a change of teacher, a
// transfer or an enrolment ties an anonymised student to a teacher: D16's
// anonymisation deleted their ties and keeps their memberships, and never
// runs for them again.
func TestAnAnonymisedMemberIsNotTiedAgain(t *testing.T) {
	w := newAdminWorld(t)
	ctx := context.Background()
	gone := w.user(t, "student", "Học viên đã ẩn danh", &w.a)
	w.exec(t, `INSERT INTO app.class_members (class_id, user_id, joined_via, added_by) VALUES ($1, $2, 'admin', $3)`, w.classA, gone, w.a)
	w.exec(t, `UPDATE app.users SET email = 'anonymous-' || id::text || '@anonymous.invalid', disabled_at = now() WHERE id = $1`, gone)
	if ok, _ := w.tied(t, w.a, gone); ok {
		t.Fatal("the anonymised member is tied before any change")
	}

	if _, err := w.svc.Commands.ChangeTeacher.Handle(ctx, command.ChangeTeacher{ClassID: w.classA, TeacherID: w.b, Actor: w.who(w.admin)}); err != nil {
		t.Fatal(err)
	}
	if ok, _ := w.tied(t, w.b, w.studentA); !ok {
		t.Error("the class's new teacher is not tied to its member")
	}
	if ok, _ := w.tied(t, w.b, gone); ok {
		t.Error("a change of teacher tied the new teacher to an anonymised member")
	}
	if _, err := repositories.NewPostgres(db.NewContext(w.tx)).TransferOwnership(ctx, w.b, w.a); err != nil {
		t.Fatal(err)
	}
	if ok, _ := w.tied(t, w.a, gone); ok {
		t.Error("a transfer tied its new teacher to an anonymised member")
	}
	if err := ties.Enrolled(ctx, w.tx, w.classB, gone); err != nil {
		t.Fatal(err)
	}
	if ok, _ := w.tied(t, w.a, gone); ok {
		t.Error("an enrolment tied an anonymised student to the class's teacher")
	}
}
