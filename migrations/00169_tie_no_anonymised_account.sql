-- +goose Up

-- D16's anonymisation deletes an account's messaging ties (T-R7.12), so an
-- anonymised student is no longer found as a former student, but it keeps the
-- account's class memberships, and it skips an account already anonymised. So
-- a tie written for an anonymised account after that stays for good. 00167
-- wrote such ties: its fill tied the members and past students of the day,
-- anonymised ones among them, and its two fill triggers tie every member of a
-- class that gets another teacher, and a new member, whoever they are. This
-- file has the triggers leave an anonymised member out, as shared/ties does
-- for this binary's own writes, and deletes the ties already written for an
-- anonymised account.
--
-- An account is anonymised when its email is the reserved address
-- (retention.AnonymousEmail). Only the member's side is read: anonymisation
-- refuses an account that teaches a class (retention.OwnsContent), and a
-- change of teacher or a transfer gives a class only to an enabled account,
-- while an anonymised one is disabled, so a class's teacher is never an
-- anonymised account.
--
-- CREATE OR REPLACE keeps each function's owner, pinned search_path and the
-- EXECUTE revoked from PUBLIC; the triggers are not touched. R8's contract
-- step still drops both. The delete reads app.users whole, once.
SET LOCAL lock_timeout = '5s';

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION app.class_members_fill_messaging_ties() RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog, pg_temp
AS $fn$
BEGIN
  INSERT INTO app.messaging_ties (staff_id, student_id)
  SELECT c.teacher_id, m.user_id
    FROM app.class_members m
    JOIN app.classes c ON c.id = m.class_id
    JOIN app.users u ON u.id = m.user_id
   WHERE m.class_id = NEW.class_id AND m.user_id = NEW.user_id AND c.teacher_id <> m.user_id
     AND u.email <> 'anonymous-' || u.id::text || '@anonymous.invalid'
     FOR SHARE OF c
  ON CONFLICT (staff_id, student_id) DO NOTHING;
  RETURN NULL;
END;
$fn$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION app.classes_fill_messaging_ties() RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog, pg_temp
AS $fn$
BEGIN
  INSERT INTO app.messaging_ties (staff_id, student_id)
  SELECT c.teacher_id, m.user_id
    FROM app.classes c
    JOIN app.class_members m ON m.class_id = c.id
    JOIN app.users u ON u.id = m.user_id
   WHERE c.id = NEW.id AND m.user_id <> c.teacher_id
     AND u.email <> 'anonymous-' || u.id::text || '@anonymous.invalid'
  ON CONFLICT (staff_id, student_id) DO NOTHING;
  RETURN NULL;
END;
$fn$;
-- +goose StatementEnd

DELETE FROM app.messaging_ties t
 USING app.users u
 WHERE u.email = 'anonymous-' || u.id::text || '@anonymous.invalid'
   AND (t.staff_id = u.id OR t.student_id = u.id);

-- +goose Down
-- The functions get 00167's bodies back. The deleted ties stay deleted: they
-- named accounts that are anonymised, which no tie should.
SET LOCAL lock_timeout = '5s';

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION app.class_members_fill_messaging_ties() RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog, pg_temp
AS $fn$
BEGIN
  INSERT INTO app.messaging_ties (staff_id, student_id)
  SELECT c.teacher_id, m.user_id
    FROM app.class_members m
    JOIN app.classes c ON c.id = m.class_id
   WHERE m.class_id = NEW.class_id AND m.user_id = NEW.user_id AND c.teacher_id <> m.user_id
     FOR SHARE OF c
  ON CONFLICT (staff_id, student_id) DO NOTHING;
  RETURN NULL;
END;
$fn$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION app.classes_fill_messaging_ties() RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog, pg_temp
AS $fn$
BEGIN
  INSERT INTO app.messaging_ties (staff_id, student_id)
  SELECT c.teacher_id, m.user_id
    FROM app.classes c
    JOIN app.class_members m ON m.class_id = c.id
   WHERE c.id = NEW.id AND m.user_id <> c.teacher_id
  ON CONFLICT (staff_id, student_id) DO NOTHING;
  RETURN NULL;
END;
$fn$;
-- +goose StatementEnd
