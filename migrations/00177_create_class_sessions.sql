-- +goose Up

-- The stored sessions of a class (T-R8.2, DG-92). A row is one of three
-- things: a one-off session (series_id and series_date NULL); a per-date
-- change to an occurrence of a series (moved, another room, a note,
-- cancelled); or an occurrence that has attendance. An occurrence of a series
-- has no row until a write needs one, and that write creates it in its own
-- transaction. series_date stays the occurrence's original date when the
-- session moves, so (series_id, series_date) names the occurrence whatever
-- starts_at says, and the API's opaque session id is {seriesId}.{yyyymmdd}
-- for an occurrence and the row's uuid for a one-off.
--
-- The composite key to (id, class_id) of the series keeps an occurrence in
-- its series' class. MATCH SIMPLE leaves a one-off, whose series_id is NULL,
-- unchecked, and the CHECK keeps series_id and series_date set together.
--
-- class_id and the series key are CASCADE: a class with sessions but no
-- attendance deletes, and so does a series that ends before its start. A row
-- with attendance holds both, because app.attendance_marks (00178) is
-- RESTRICT. created_by is RESTRICT, as every authored row's is.
--
-- The foreign keys lock app.users, app.classes and app.class_session_series
-- while the file runs, so lock_timeout fails it at once rather than queue
-- writers behind it.
SET LOCAL lock_timeout = '5s';

CREATE TABLE app.class_sessions (
  id               uuid PRIMARY KEY DEFAULT uuidv7(),
  class_id         uuid NOT NULL REFERENCES app.classes(id) ON DELETE CASCADE,
  series_id        uuid,
  series_date      date,
  starts_at        timestamptz NOT NULL,
  duration_minutes smallint NOT NULL CHECK (duration_minutes BETWEEN 15 AND 480),
  room             text CHECK (char_length(room) BETWEEN 1 AND 60),
  note             text CHECK (char_length(note) BETWEEN 1 AND 200),
  cancelled_at     timestamptz,
  created_by       uuid NOT NULL REFERENCES app.users(id) ON DELETE RESTRICT,
  created_at       timestamptz NOT NULL DEFAULT now(),
  updated_at       timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT class_sessions_occurrence_check CHECK ((series_id IS NULL) = (series_date IS NULL)),
  CONSTRAINT class_sessions_series_id_class_id_fkey FOREIGN KEY (series_id, class_id)
    REFERENCES app.class_session_series (id, class_id) ON DELETE CASCADE,
  -- One row per occurrence: the target of attendance's
  -- INSERT ... ON CONFLICT (series_id, series_date) DO NOTHING (T-R8.6), and
  -- the index the series key's cascade reads.
  CONSTRAINT class_sessions_series_id_series_date_key UNIQUE (series_id, series_date)
);

-- A class's sessions in a range, for the calendar and the attendance list.
CREATE INDEX class_sessions_class_starts_idx ON app.class_sessions (class_id, starts_at);

CREATE TRIGGER class_sessions_set_updated_at BEFORE UPDATE ON app.class_sessions
  FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();

-- quizzivy_app holds SELECT, INSERT, UPDATE and DELETE through the default
-- privileges of 00009: a teacher adds, changes, cancels and removes sessions.

-- +goose Down
SET LOCAL lock_timeout = '5s';

DROP TABLE app.class_sessions;
