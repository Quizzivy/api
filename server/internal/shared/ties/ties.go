// Package ties records who has taught whom in app.messaging_ties (T-R7.9), so
// a staff member may write first to a student who has left their class
// (O-R7.8). Every write that puts a student in a class, or gives a class
// another teacher, calls it in the same transaction. Leaving a class removes
// nothing, and a pair already known keeps the time it was first known.
package ties

import (
	"context"
	"fmt"

	"quizzivy/internal/shared/audit"
)

// Enrolled records that classID's teacher teaches studentID, after a write
// that put the student in the class.
func Enrolled(ctx context.Context, db audit.Execer, classID, studentID string) error {
	if _, err := db.Exec(ctx, `
		INSERT INTO app.messaging_ties (staff_id, student_id)
		SELECT c.teacher_id, $2::uuid FROM app.classes c
		 WHERE c.id = $1::uuid AND c.teacher_id <> $2::uuid
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
		 WHERE c.id = ANY($1::uuid[]) AND m.user_id <> c.teacher_id
		ON CONFLICT (staff_id, student_id) DO NOTHING`, classIDs); err != nil {
		return fmt.Errorf("ties: record the members of %d reassigned classes: %w", len(classIDs), err)
	}
	return nil
}
