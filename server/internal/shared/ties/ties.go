// Package ties records who has taught whom in app.messaging_ties (T-R7.9), so
// a thread tells a former student or a former teacher apart from one the
// other side never taught. Since F2 a tie no longer decides whom staff may
// write to: any staff member may write to any enabled student. Every write that puts a student in a class, or gives a class
// another teacher, calls it in the same transaction. Leaving a class removes
// nothing, and a pair already known keeps the time it was first known. An
// anonymised student is never tied: D16's anonymisation deletes the account's
// ties but keeps its memberships, and it does not run again for the account,
// so a tie written after it would stay for good.
package ties

import (
	"context"
	"fmt"

	"quizzivy/internal/shared/audit"
	"quizzivy/internal/shared/retention"
)

// Enrolled records that classID's teacher teaches studentID, after a write
// that put the student in the class. It reads the teacher with the class held
// FOR SHARE, which a change of teacher's FOR NO KEY UPDATE waits for and which
// waits for it: the member's foreign key takes only FOR KEY SHARE, which does
// not. So a change that commits first is the teacher read here, and one that
// comes later sees the new member when it calls Reassigned. The caller takes
// that lock before any join code row, in the order Rotate and Revoke take the
// class and the code, or a redemption and a rotation can deadlock.
func Enrolled(ctx context.Context, db audit.Execer, classID, studentID string) error {
	if _, err := db.Exec(ctx, `
		INSERT INTO app.messaging_ties (staff_id, student_id)
		SELECT c.teacher_id, u.id FROM app.classes c JOIN app.users u ON u.id = $2::uuid
		 WHERE c.id = $1::uuid AND c.teacher_id <> u.id AND `+retention.NotAnonymous+`
		   FOR SHARE OF c
		ON CONFLICT (staff_id, student_id) DO NOTHING`, classID, studentID); err != nil {
		return fmt.Errorf("ties: record %s in class %s: %w", studentID, classID, err)
	}
	return nil
}

// Reassigned records that each class's teacher teaches every member it has
// now, after a write that gave the classes another teacher.
func Reassigned(ctx context.Context, db audit.Execer, classIDs []string) error {
	if len(classIDs) == 0 {
		return nil
	}
	if _, err := db.Exec(ctx, `
		INSERT INTO app.messaging_ties (staff_id, student_id)
		SELECT c.teacher_id, m.user_id
		  FROM app.classes c
		  JOIN app.class_members m ON m.class_id = c.id
		  JOIN app.users u ON u.id = m.user_id
		 WHERE c.id = ANY($1::uuid[]) AND m.user_id <> c.teacher_id AND `+retention.NotAnonymous+`
		ON CONFLICT (staff_id, student_id) DO NOTHING`, classIDs); err != nil {
		return fmt.Errorf("ties: record the members of %d reassigned classes: %w", len(classIDs), err)
	}
	return nil
}
