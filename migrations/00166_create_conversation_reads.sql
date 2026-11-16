-- +goose Up

-- How far each reader has read a conversation (T-R7.9): the newest message
-- they have seen. Unread counts, "Seen by" and the in-app message item compare
-- it with app.messages.id in uuidv7 order (T-R7.10a, T-R7.12), and the pointer
-- only moves forward. It has no foreign key: the order is what counts, and the
-- check that the message belongs to the conversation is the command's (422).
-- A conversation's or a reader's removal takes the row.
--
-- The foreign keys lock app.users and app.conversations while the file runs,
-- so lock_timeout fails it at once rather than queue writers behind it.
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

CREATE TABLE app.conversation_reads (
  conversation_id      uuid NOT NULL REFERENCES app.conversations(id) ON DELETE CASCADE,
  user_id              uuid NOT NULL REFERENCES app.users(id) ON DELETE CASCADE,
  last_read_message_id uuid NOT NULL,
  read_at              timestamptz NOT NULL DEFAULT now(),

  PRIMARY KEY (conversation_id, user_id)
);

-- quizzivy_app holds SELECT, INSERT, UPDATE and DELETE through the default
-- privileges of 00009. A pointer is moved, never removed.
REVOKE DELETE ON app.conversation_reads FROM quizzivy_app;

-- D16's anonymisation (T-R7.12) deletes the pointers of the threads it removes
-- and the account's own.
GRANT SELECT (conversation_id, user_id), DELETE ON app.conversation_reads TO quizzivy_maintenance;

-- +goose Down
SET LOCAL lock_timeout = '5s';

DROP TABLE app.conversation_reads;
