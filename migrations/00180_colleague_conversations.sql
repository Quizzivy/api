-- +goose Up

-- F2's colleague threads (T-F2.2, docs/plan/77g-f2.md): a direct thread
-- between two staff members, a third kind of app.conversations beside a
-- class's channel and a staff member's thread with a student (00164).
--
-- The pair has its own two columns, the smaller uuid first, so one CHECK and
-- one partial unique index hold one thread per pair whoever writes first, and
-- no query that reads staff_id or student_id (the student's list, unread,
-- notices, anonymisation, the export) ever meets a colleague row. Both keys are
-- RESTRICT, as staff_id and student_id are: deleteUser answers 409 with
-- referencedBy messages (identity/repositories/delete.go).
--
-- Every existing row is a class or a direct thread with both new columns
-- NULL, so the widened kind check and the two shape checks are added NOT VALID
-- and validated in the same file, as 00174 does: the validation finds nothing
-- to refuse. The file is one transaction, so the ACCESS EXCLUSIVE lock the
-- ALTERs take is held to its commit; the table holds one row per thread, so
-- that is a moment, and the indexes are built in place, not CONCURRENTLY. The
-- v0.15.0 pods of the rolling deploy name no new column, so their inserts
-- write both NULL and pass every check.
--
-- R8's 00179 dropped 00167's tie fill triggers, so nothing here touches them.
--
-- The foreign keys lock app.users while the file runs, so lock_timeout fails it
-- at once rather than queue writers behind it.
SET LOCAL lock_timeout = '5s';

ALTER TABLE app.conversations DROP CONSTRAINT conversations_kind_check;
ALTER TABLE app.conversations
  ADD CONSTRAINT conversations_kind_check CHECK (kind IN ('class', 'direct', 'colleague')) NOT VALID;
ALTER TABLE app.conversations VALIDATE CONSTRAINT conversations_kind_check;

ALTER TABLE app.conversations
  ADD COLUMN colleague_low_id uuid
    CONSTRAINT conversations_colleague_low_id_fkey REFERENCES app.users(id) ON DELETE RESTRICT,
  ADD COLUMN colleague_high_id uuid
    CONSTRAINT conversations_colleague_high_id_fkey REFERENCES app.users(id) ON DELETE RESTRICT;

-- A colleague thread names its pair and nothing else, ordered low-first, which
-- also keeps the two apart; any other kind names no colleague.
ALTER TABLE app.conversations
  ADD CONSTRAINT conversations_colleague_shape CHECK (
    kind <> 'colleague' OR (
      class_id IS NULL AND staff_id IS NULL AND student_id IS NULL
      AND colleague_low_id IS NOT NULL AND colleague_high_id IS NOT NULL
      AND colleague_low_id < colleague_high_id
    )
  ) NOT VALID,
  ADD CONSTRAINT conversations_colleague_columns CHECK (
    kind = 'colleague' OR (colleague_low_id IS NULL AND colleague_high_id IS NULL)
  ) NOT VALID;
ALTER TABLE app.conversations VALIDATE CONSTRAINT conversations_colleague_shape;
ALTER TABLE app.conversations VALIDATE CONSTRAINT conversations_colleague_columns;

-- One thread per pair, the target of the first post's ON CONFLICT (T-F2.4),
-- and each side's list, newest post first.
CREATE UNIQUE INDEX conversations_colleague_key ON app.conversations (colleague_low_id, colleague_high_id)
  WHERE kind = 'colleague';
CREATE INDEX conversations_colleague_low_idx ON app.conversations (colleague_low_id, last_message_at DESC)
  WHERE kind = 'colleague';
CREATE INDEX conversations_colleague_high_idx ON app.conversations (colleague_high_id, last_message_at DESC)
  WHERE kind = 'colleague';

-- quizzivy_app keeps the table-wide SELECT and INSERT that cover the new
-- columns, and UPDATE on last_message_at alone (00164): nothing is granted.
-- D16's anonymisation finds a disabled account's colleague threads by their
-- pair (T-F2.5).
GRANT SELECT (colleague_low_id, colleague_high_id) ON app.conversations TO quizzivy_maintenance;

-- +goose Down
SET LOCAL lock_timeout = '5s';

-- Refuses while a colleague thread exists, as 00174 refuses for a flexible
-- assignment: the v0.15.0 binary cannot read one, and its pair cannot be
-- rebuilt once the columns go.
-- +goose StatementBegin
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM app.conversations WHERE kind = 'colleague') THEN
    RAISE EXCEPTION 'a colleague conversation exists; dropping its columns would lose the thread';
  END IF;
END
$$;
-- +goose StatementEnd

REVOKE SELECT (colleague_low_id, colleague_high_id) ON app.conversations FROM quizzivy_maintenance;
DROP INDEX app.conversations_colleague_high_idx;
DROP INDEX app.conversations_colleague_low_idx;
DROP INDEX app.conversations_colleague_key;
ALTER TABLE app.conversations
  DROP CONSTRAINT conversations_colleague_columns,
  DROP CONSTRAINT conversations_colleague_shape;
ALTER TABLE app.conversations DROP COLUMN colleague_high_id, DROP COLUMN colleague_low_id;
ALTER TABLE app.conversations DROP CONSTRAINT conversations_kind_check;
ALTER TABLE app.conversations ADD CONSTRAINT conversations_kind_check CHECK (kind IN ('class', 'direct'));
