-- +goose Up

-- R8's contract step for T-R7.9 (plan 70, "Expand, then contract"). 00167 added
-- two fill triggers for the R6 binary, which wrote no tie while it served
-- beside the R7 one. Every binary since v0.14.0 writes its own ties in the
-- transaction that enrols a student or gives a class another teacher
-- (shared/ties: Enrolled from the classes module's join with a code and its
-- add-member, and from identity's create user, create student and user
-- import; Reassigned from the classes module's change of teacher and its
-- transfer of ownership), and v0.14.1, the binary that serves while
-- this file runs, is one of them. So the triggers and their
-- functions go; app.messaging_ties and its rows stay.
--
-- A trigger goes before its function. DROP TRIGGER takes ACCESS EXCLUSIVE on
-- its table and touches the catalog only, and it waits for any open
-- transaction that wrote a member or a teacher, whose deferred trigger then
-- fires at its commit as before. lock_timeout turns a lock that does not
-- arrive within five seconds into a failed migration rather than queue every
-- writer of class_members and classes behind it. No IF EXISTS: a wrong name
-- must fail the file, not leave an object behind.
SET LOCAL lock_timeout = '5s';

DROP TRIGGER classes_fill_messaging_ties ON app.classes;
DROP TRIGGER class_members_fill_messaging_ties ON app.class_members;
DROP FUNCTION app.classes_fill_messaging_ties();
DROP FUNCTION app.class_members_fill_messaging_ties();

-- +goose Down

-- Restores the functions and triggers as 00167 created them, with the bodies
-- 00169 gave them (an anonymised member is not tied): pinned search_path,
-- EXECUTE revoked from PUBLIC, deferred to the commit. The ties written while
-- they were gone stay; the binary wrote them. Nothing in production runs it.
SET LOCAL lock_timeout = '5s';

-- +goose StatementBegin
CREATE FUNCTION app.class_members_fill_messaging_ties() RETURNS trigger
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
CREATE FUNCTION app.classes_fill_messaging_ties() RETURNS trigger
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

REVOKE ALL ON FUNCTION app.class_members_fill_messaging_ties() FROM PUBLIC;
REVOKE ALL ON FUNCTION app.classes_fill_messaging_ties() FROM PUBLIC;

CREATE CONSTRAINT TRIGGER class_members_fill_messaging_ties
  AFTER INSERT ON app.class_members
  DEFERRABLE INITIALLY DEFERRED
  FOR EACH ROW EXECUTE FUNCTION app.class_members_fill_messaging_ties();
CREATE CONSTRAINT TRIGGER classes_fill_messaging_ties
  AFTER UPDATE OF teacher_id ON app.classes
  DEFERRABLE INITIALLY DEFERRED
  FOR EACH ROW WHEN (OLD.teacher_id IS DISTINCT FROM NEW.teacher_id)
  EXECUTE FUNCTION app.classes_fill_messaging_ties();
