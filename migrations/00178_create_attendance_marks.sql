-- +goose Up

-- A student's attendance at one session (T-R8.2, DG-92): present, late or
-- absent. Unmarked is the absence of a row, and unmarking deletes it; excused
-- is not built (O-R8.1). note is v2's per-mark note (T-R8.6), seen by the
-- teachers only. Every change is audited by T-R8.6's attendance.saved.
--
-- Every key is RESTRICT. session_id holds the session row, and through it the
-- class: a class with attendance is never hard-deleted, as one with attempts
-- is not, and deleteClass answers 409 with referencedBy attendance. student_id
-- and marked_by hold the accounts the same way for deleteUser.
--
-- The foreign keys lock app.users and app.class_sessions while the file runs,
-- so lock_timeout fails it at once rather than queue writers behind it.
SET LOCAL lock_timeout = '5s';

CREATE TABLE app.attendance_marks (
  session_id uuid NOT NULL REFERENCES app.class_sessions(id) ON DELETE RESTRICT,
  student_id uuid NOT NULL REFERENCES app.users(id) ON DELETE RESTRICT,
  status     text NOT NULL CHECK (status IN ('present', 'late', 'absent')),
  marked_by  uuid NOT NULL REFERENCES app.users(id) ON DELETE RESTRICT,
  marked_at  timestamptz NOT NULL,
  note       text CHECK (char_length(note) BETWEEN 1 AND 500),

  PRIMARY KEY (session_id, student_id)
);

-- A student's marks, for the rate and the RESTRICT key on deleteUser.
CREATE INDEX attendance_marks_student_idx ON app.attendance_marks (student_id);

-- quizzivy_app holds SELECT, INSERT, UPDATE and DELETE through the default
-- privileges of 00009: a teacher marks, changes and unmarks.

-- +goose Down
SET LOCAL lock_timeout = '5s';

DROP TABLE app.attendance_marks;
