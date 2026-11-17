-- +goose Up

-- A thread of messages (T-R7.9, D17): a class's announcement channel, or a
-- direct thread between one staff member and one student. The kind decides
-- which columns are set, and a CHECK per kind holds them to it. Each class has
-- at most one channel and each pair at most one direct thread, so the first
-- post creates the row with INSERT ... ON CONFLICT DO NOTHING on the partial
-- unique index of its kind (T-R7.10a), and nothing creates one before.
--
-- class_id is RESTRICT: a class whose channel has posts is never hard-deleted,
-- and deleteClass answers 409 with referencedBy messages. staff_id and
-- student_id are RESTRICT for the same reason on deleteUser. A thread outlives
-- the class that tied its two participants (T-R7.10a).
--
-- A conversation is never deleted by the app: only D16's anonymisation removes
-- an account's direct threads, as quizzivy_maintenance (T-R7.12). The app
-- changes nothing but last_message_at, which each post moves.
--
-- The foreign keys lock app.users and app.classes while the file runs, so
-- lock_timeout fails it at once rather than queue writers behind it.
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

CREATE TABLE app.conversations (
  id              uuid PRIMARY KEY DEFAULT uuidv7(),
  kind            text NOT NULL,
  class_id        uuid REFERENCES app.classes(id) ON DELETE RESTRICT,
  staff_id        uuid REFERENCES app.users(id) ON DELETE RESTRICT,
  student_id      uuid REFERENCES app.users(id) ON DELETE RESTRICT,
  created_at      timestamptz NOT NULL DEFAULT now(),
  last_message_at timestamptz NOT NULL,

  CONSTRAINT conversations_kind_check CHECK (kind IN ('class', 'direct')),
  CONSTRAINT conversations_class_shape
    CHECK (kind <> 'class' OR (class_id IS NOT NULL AND staff_id IS NULL AND student_id IS NULL)),
  CONSTRAINT conversations_direct_shape
    CHECK (kind <> 'direct' OR (class_id IS NULL AND staff_id IS NOT NULL AND student_id IS NOT NULL)),
  CONSTRAINT conversations_participants_differ CHECK (staff_id <> student_id)
);

-- One channel per class, and one direct thread per pair: the targets of the
-- first post's ON CONFLICT. The second also finds a staff member's thread with
-- one student.
CREATE UNIQUE INDEX conversations_class_key ON app.conversations (class_id) WHERE kind = 'class';
CREATE UNIQUE INDEX conversations_direct_key ON app.conversations (staff_id, student_id) WHERE kind = 'direct';

-- Each side's thread list, newest post first.
CREATE INDEX conversations_staff_idx ON app.conversations (staff_id, last_message_at DESC)
  WHERE kind = 'direct';
CREATE INDEX conversations_student_idx ON app.conversations (student_id, last_message_at DESC)
  WHERE kind = 'direct';

-- quizzivy_app holds SELECT, INSERT, UPDATE and DELETE through the default
-- privileges of 00009. It keeps SELECT and INSERT, and UPDATE on
-- last_message_at alone.
REVOKE UPDATE, DELETE ON app.conversations FROM quizzivy_app;
GRANT UPDATE (last_message_at) ON app.conversations TO quizzivy_app;

-- D16's anonymisation (T-R7.12) finds a disabled account's direct threads by
-- their participants and deletes them.
GRANT SELECT (id, kind, staff_id, student_id), DELETE ON app.conversations TO quizzivy_maintenance;

-- +goose Down
SET LOCAL lock_timeout = '5s';

DROP TABLE app.conversations;
