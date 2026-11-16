-- +goose Up

-- A message of a conversation (T-R7.9, D17): a body, a file, or both. The body
-- is stored trimmed, 1 to 4000 characters; a file is a row of
-- app.message_attachments that its sender uploaded, and UNIQUE keeps one file
-- to one message. client_id is made by the client for each message, so a
-- retried send finds the message it made under (sender_id, client_id) instead
-- of posting twice.
--
-- Append-only for the app role, as audit_log and attempt_events are: nothing
-- edits or deletes a message, because the log of teacher-student contact
-- protects both sides and most students are minors. The one deletion is D16's
-- anonymisation of an account's direct threads, as quizzivy_maintenance
-- (T-R7.12). Posts are not audited: these rows are the record, readable by
-- their participants only.
--
-- Every key is RESTRICT: a message keeps its thread, its sender and its file.
-- The foreign keys lock app.users, app.conversations and
-- app.message_attachments while the file runs, so lock_timeout fails it at once
-- rather than queue writers behind it.
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

CREATE TABLE app.messages (
  id              uuid PRIMARY KEY DEFAULT uuidv7(),
  conversation_id uuid NOT NULL REFERENCES app.conversations(id) ON DELETE RESTRICT,
  sender_id       uuid NOT NULL REFERENCES app.users(id) ON DELETE RESTRICT,
  client_id       uuid NOT NULL,
  body            text,
  attachment_id   uuid UNIQUE REFERENCES app.message_attachments(id) ON DELETE RESTRICT,
  created_at      timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT messages_body_check
    CHECK (char_length(body) BETWEEN 1 AND 4000 AND body = btrim(body, E' \t\n\r')),
  CONSTRAINT messages_has_content CHECK (body IS NOT NULL OR attachment_id IS NOT NULL),
  CONSTRAINT messages_sender_client_key UNIQUE (sender_id, client_id)
);

-- A thread's page, newest first by uuidv7 order, which is the order of posts
-- and of the read pointers compared with them (00166).
CREATE INDEX messages_conversation_idx ON app.messages (conversation_id, id DESC);

-- 00009 grants every table's four privileges by default; append-only is a
-- privilege here, as for app.attempt_events.
REVOKE UPDATE, DELETE ON app.messages FROM quizzivy_app;

-- D16's anonymisation (T-R7.12) deletes the messages of a disabled account's
-- direct threads and the files they hold. It reads the columns that find them,
-- never a body (00139's rule, and D17).
GRANT SELECT (id, conversation_id, attachment_id), DELETE ON app.messages TO quizzivy_maintenance;

-- +goose Down
SET LOCAL lock_timeout = '5s';

DROP TABLE app.messages;
