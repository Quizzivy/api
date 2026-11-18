-- +goose Up

-- F1's assignment modes and time on the paper (T-F1.2,
-- docs/plan/77f-f1-assignment-modes.md).
--
-- assignments.mode: strict is the product as it was at v0.14.0; flexible
-- makes an attempt's deadline its student's close and never acts on an
-- absence. A constant default is metadata only on PostgreSQL 18, so no row is
-- rewritten, and the v0.14.0 pod's insert, which names no such column, writes
-- strict during the rolling deploy. duration_minutes and the leaving rule
-- stay NOT NULL and are stored, unapplied, under flexible.
--
-- attempts.active_ms and active_reported_at: the paper's visible time as the
-- engine reports it, capped by the server's clock. NULL is "not measured": a
-- row from before F1, and one the v0.14.0 pod inserts during the deploy,
-- which names neither column. The check is added NOT VALID and validated in
-- the same file, as 00147 does; every row is NULL, so the validation finds
-- nothing, and lock_timeout fails the file at once rather than queue writers
-- behind the ACCESS EXCLUSIVE the ALTERs take.
SET LOCAL lock_timeout = '5s';

CREATE TYPE app.assignment_mode AS ENUM ('strict', 'flexible');

ALTER TABLE app.assignments ADD COLUMN mode app.assignment_mode NOT NULL DEFAULT 'strict';

ALTER TABLE app.attempts
  ADD COLUMN active_ms bigint,
  ADD COLUMN active_reported_at timestamptz;
ALTER TABLE app.attempts
  ADD CONSTRAINT attempts_active_ms_check CHECK (active_ms >= 0) NOT VALID;
ALTER TABLE app.attempts VALIDATE CONSTRAINT attempts_active_ms_check;

-- window-schedule recomputes a flexible attempt's deadline from its
-- student's close instead of adding the window's length (core/maintenance,
-- ScheduleWindow), so it reads the mode beside the columns 00139 grants.
GRANT SELECT (mode) ON app.assignments TO quizzivy_maintenance;

-- +goose Down
SET LOCAL lock_timeout = '5s';

REVOKE SELECT (mode) ON app.assignments FROM quizzivy_maintenance;
ALTER TABLE app.attempts DROP COLUMN active_reported_at, DROP COLUMN active_ms;
ALTER TABLE app.assignments DROP COLUMN mode;
DROP TYPE app.assignment_mode;
