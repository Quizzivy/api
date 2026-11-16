-- +goose Up

-- Who last wrote a test or a bank question, so a co-editor's stale write can
-- name them (T-R7.1, T-R7.5). Nullable and unconstrained beyond the foreign
-- key, so the R6 binary keeps inserting and updating during the rolling deploy
-- without a fill trigger. It never names the column: a row it inserts is
-- NULL, which the stale-write message reads as "someone", and nothing else
-- depends on it. A row it updates keeps the writer it had while the trigger
-- moves updated_at. Before any R7 write that writer is NULL too; for a row R7
-- wrote first (the deploy's overlap, or every row after a rollback to v0.13)
-- a stale write names the earlier R7 writer at the time of v0.13's save.
--
-- After a rollback, before R7 takes traffic again, clear what v0.13 wrote
-- since the rollback, with the updated_at triggers disabled as 00060 does so
-- that no open editor's revision moves:
--   UPDATE app.tests SET updated_by = NULL WHERE updated_at >= :rolled_back_at;
--   UPDATE app.questions SET updated_by = NULL WHERE updated_at >= :rolled_back_at;
--
-- Each foreign key is added NOT VALID and validated in the same file, as R6
-- does. The columns are all NULL, so the validation has nothing to check, and
-- lock_timeout fails the file at once rather than queue writers behind the
-- ACCESS EXCLUSIVE the ALTERs take.
SET LOCAL lock_timeout = '5s';

ALTER TABLE app.tests ADD COLUMN updated_by uuid;
ALTER TABLE app.tests
  ADD CONSTRAINT tests_updated_by_fkey FOREIGN KEY (updated_by)
    REFERENCES app.users(id) ON DELETE SET NULL NOT VALID;
ALTER TABLE app.tests VALIDATE CONSTRAINT tests_updated_by_fkey;

ALTER TABLE app.questions ADD COLUMN updated_by uuid;
ALTER TABLE app.questions
  ADD CONSTRAINT questions_updated_by_fkey FOREIGN KEY (updated_by)
    REFERENCES app.users(id) ON DELETE SET NULL NOT VALID;
ALTER TABLE app.questions VALIDATE CONSTRAINT questions_updated_by_fkey;

-- +goose Down
SET LOCAL lock_timeout = '5s';

ALTER TABLE app.questions DROP COLUMN updated_by;
ALTER TABLE app.tests DROP COLUMN updated_by;
