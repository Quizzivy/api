-- +goose Up

-- A class's weekly session (T-R8.2, DG-92): one weekday, start time and length,
-- from a first date to an optional last one, in the time zone of the teacher
-- who made it. An occurrence has no row of its own until it needs one
-- (app.class_sessions, 00177): reads compute the occurrences of the range a
-- screen asks for, and never write.
--
-- weekday is ISO numbering, 1 for Monday to 7 for Sunday. The time zone's
-- validity is checked in Go (time.LoadLocation); the column only bounds its
-- length, as users.time_zone does. A series runs at most two years
-- (731 days); one without an end is bounded at read time by the requested
-- range, and the command refuses a first date more than two years ahead.
--
-- class_id is CASCADE: a class with sessions but no attendance deletes, and
-- its series with it. UNIQUE (id, class_id) is the target of the occurrence
-- rows' composite key, so an occurrence never belongs to another class.
-- created_by is RESTRICT, as every authored row's is.
--
-- The foreign keys lock app.users and app.classes while the file runs, so
-- lock_timeout fails it at once rather than queue writers behind it.
SET LOCAL lock_timeout = '5s';

CREATE TABLE app.class_session_series (
  id               uuid PRIMARY KEY DEFAULT uuidv7(),
  class_id         uuid NOT NULL REFERENCES app.classes(id) ON DELETE CASCADE,
  weekday          smallint NOT NULL CHECK (weekday BETWEEN 1 AND 7),
  start_time       time NOT NULL,
  duration_minutes smallint NOT NULL CHECK (duration_minutes BETWEEN 15 AND 480),
  room             text CHECK (char_length(room) BETWEEN 1 AND 60),
  starts_on        date NOT NULL,
  ends_on          date,
  time_zone        text NOT NULL CHECK (char_length(time_zone) BETWEEN 1 AND 64),
  created_by       uuid NOT NULL REFERENCES app.users(id) ON DELETE RESTRICT,
  created_at       timestamptz NOT NULL DEFAULT now(),
  updated_at       timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT class_session_series_ends_on_check CHECK (ends_on IS NULL OR ends_on >= starts_on),
  CONSTRAINT class_session_series_two_years CHECK (ends_on IS NULL OR ends_on <= starts_on + 731),
  CONSTRAINT class_session_series_id_class_id_key UNIQUE (id, class_id)
);

-- A class's series, for the calendar, the class's schedule line and the class
-- delete's cascade.
CREATE INDEX class_session_series_class_idx ON app.class_session_series (class_id);

CREATE TRIGGER class_session_series_set_updated_at BEFORE UPDATE ON app.class_session_series
  FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();

-- quizzivy_app holds SELECT, INSERT, UPDATE and DELETE through the default
-- privileges of 00009: a teacher creates, splits, ends and deletes a series.

-- +goose Down
SET LOCAL lock_timeout = '5s';

DROP TABLE app.class_session_series;
