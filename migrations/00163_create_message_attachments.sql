-- +goose Up

-- A file a staff member attaches to a message (T-R7.9, D17): PDF, Word (DOCX
-- or DOC), PNG, JPEG, MP3 or M4A, at most 20 MiB, typed by sniffing its content
-- (T-R7.11). WebP is refused: an image must pass image.DecodeConfig's
-- dimension cap, and the standard library decodes no WebP. Students attach
-- nothing. The object lives in storage at storage_key; this row is what a
-- message names and what the download checks.
--
-- A row is made by the upload, before any message names it, and the daily
-- housekeeping removes one left unsent for 24 hours (T-R7.11). Once a message
-- names it, the RESTRICT key from app.messages (00165) keeps it, so the app
-- role's DELETE cannot remove a sent file. A row is never changed.
--
-- The foreign key locks app.users while the file runs, so lock_timeout fails it
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

CREATE TABLE app.message_attachments (
  id                uuid PRIMARY KEY DEFAULT uuidv7(),
  uploaded_by       uuid NOT NULL REFERENCES app.users(id) ON DELETE RESTRICT,
  storage_key       text NOT NULL UNIQUE,
  mime_type         text NOT NULL,
  bytes             integer NOT NULL,
  original_filename text NOT NULL,
  checksum_sha256   bytea NOT NULL,
  created_at        timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT message_attachments_mime_type_check CHECK (mime_type IN (
    'application/pdf',
    'application/vnd.openxmlformats-officedocument.wordprocessingml.document',
    'application/msword',
    'image/png',
    'image/jpeg',
    'audio/mpeg',
    'audio/mp4')),
  CONSTRAINT message_attachments_bytes_check CHECK (bytes BETWEEN 1 AND 20971520),
  CONSTRAINT message_attachments_original_filename_check
    CHECK (char_length(original_filename) BETWEEN 1 AND 200),
  CONSTRAINT message_attachments_checksum_sha256_check CHECK (length(checksum_sha256) = 32)
);

-- An uploader's files by age: the sweep of unsent files and the cascade check
-- of a user's deletion.
CREATE INDEX message_attachments_uploader_idx ON app.message_attachments (uploaded_by, created_at);

-- quizzivy_app holds SELECT, INSERT, UPDATE and DELETE through the default
-- privileges of 00009. A file is never rewritten.
REVOKE UPDATE ON app.message_attachments FROM quizzivy_app;

-- D16's anonymisation (T-R7.12, cmd/maintenance) deletes a disabled account's
-- files with its direct conversations. It reads the columns that find them and
-- nothing a person typed or the object's key (00139's rule): the objects are
-- removed by the housekeeping sweep, which holds the storage credentials.
GRANT SELECT (id, uploaded_by), DELETE ON app.message_attachments TO quizzivy_maintenance;

-- +goose Down
SET LOCAL lock_timeout = '5s';

DROP TABLE app.message_attachments;
