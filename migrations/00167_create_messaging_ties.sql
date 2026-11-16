-- +goose Up

-- Who has taught whom (T-R7.9, O-R7.8). A staff member may write first to a
-- student who is, or has been, in a class of theirs (the owner, 2026-11-13,
-- Q5 of R7 Messages.dc.html), and app.class_members forgets a student who
-- leaves: the row is deleted. This table remembers the pair. The classes
-- module adds a row in the transaction that puts a student in a class and in
-- the one that gives a class another teacher, for each of its members. Leaving
-- a class removes nothing. since is when the pair was first known.
--
-- Up fills it from today's members joined to their class's teacher, and from
-- the audit log's class.member_enrolled rows, whose diff names the student and
-- the class: a student who joined with a code and has left since. A student
-- staff added and removed before this file is not known as former, because
-- those rows name the class only (O-R7.8). Both join the class's teacher of
-- today. The audit log has no index on action, so the fill reads it whole,
-- once, under ACCESS SHARE, which no writer waits for.
--
-- A row is a fact, so it is never changed: the app role inserts and reads.
-- D16's anonymisation removes an account's rows (T-R7.12), so an anonymised
-- student is no longer found as a former student. Either user's deletion takes
-- the row.
--
-- The foreign keys lock app.users while the file runs, so lock_timeout fails it
-- at once rather than queue writers behind it.
SET LOCAL lock_timeout = '5s';

-- +goose StatementBegin
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'quizzivy_maintenance') THEN
    RAISE EXCEPTION 'role quizzivy_maintenance does not exist: run scripts/provision-db.sh first'
      USING ERRCODE = 'undefined_object';
  END IF;
END
$$;
-- +goose StatementEnd

CREATE TABLE app.messaging_ties (
  staff_id   uuid NOT NULL REFERENCES app.users(id) ON DELETE CASCADE,
  student_id uuid NOT NULL REFERENCES app.users(id) ON DELETE CASCADE,
  since      timestamptz NOT NULL DEFAULT now(),

  PRIMARY KEY (staff_id, student_id),
  CONSTRAINT messaging_ties_participants_differ CHECK (staff_id <> student_id)
);

-- The ids are compared as text, so a diff that does not hold a uuid joins
-- nothing instead of failing the file.
INSERT INTO app.messaging_ties (staff_id, student_id, since)
SELECT taught.staff_id, taught.student_id, min(taught.since)
  FROM (
    SELECT c.teacher_id, m.user_id, m.joined_at
      FROM app.class_members m
      JOIN app.classes c ON c.id = m.class_id
    UNION ALL
    SELECT c.teacher_id, u.id, a.occurred_at
      FROM app.audit_log a
      JOIN app.classes c ON c.id::text = a.diff ->> 'class_id'
      JOIN app.users u ON u.id::text = a.diff ->> 'user_id'
     WHERE a.action = 'class.member_enrolled'
  ) AS taught (staff_id, student_id, since)
 WHERE taught.staff_id <> taught.student_id
 GROUP BY taught.staff_id, taught.student_id;

-- quizzivy_app holds SELECT, INSERT, UPDATE and DELETE through the default
-- privileges of 00009. It keeps SELECT and INSERT.
REVOKE UPDATE, DELETE ON app.messaging_ties FROM quizzivy_app;

-- D16's anonymisation (T-R7.12) deletes an account's ties.
GRANT SELECT (staff_id, student_id), DELETE ON app.messaging_ties TO quizzivy_maintenance;

-- +goose Down
SET LOCAL lock_timeout = '5s';

DROP TABLE app.messaging_ties;
