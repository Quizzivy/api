-- +goose Up

-- Who last wrote a test or a bank question, so a co-editor's stale write can
-- name them (T-R7.1, T-R7.5). Nullable and unconstrained beyond the foreign
-- key, so the R6 binary keeps inserting and updating during the rolling deploy
-- without a fill trigger: it leaves the column NULL, which the stale-write
-- message reads as "someone", and nothing else depends on it.
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
