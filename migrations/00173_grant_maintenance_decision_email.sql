-- +goose Up

-- The owner's decision of 2026-11-16: `maintenance ai-credits approve` and
-- `decline` with -apply email the teacher, as the Admin operation does
-- (T-R7.23), instead of telling them in the app alone. The console hands the
-- decision to the notifications module's Notify with its email channel, which
-- queues the email in R5's outbox and sends nothing itself: the API's and the
-- worker's dispatchers send the row. These are the least grants that path
-- runs (notifications/repositories.Mail.Enqueue and email.Outbox.Enqueue);
-- every other column of each table stays out of the role's reach, and the
-- role still cannot read, change or delete a queued row, nor touch the
-- suppression list, which only the dispatcher reads. The grants fail when
-- the role is missing, as 00139's do.

-- The recipient query (repositories.decide) reads the email column of the
-- recipient's saved switch beside the in_app column 00139 grants. The
-- decision email is governed by no switch, so it joins no row, but the query
-- names the column all the same.
GRANT SELECT (email) ON app.notification_preferences TO quizzivy_maintenance;

-- The outbox's INSERT takes the recipient's own language when the message
-- names none, from users.locale; 00139 grants the other columns it reads.
GRANT SELECT (locale) ON app.users TO quizzivy_maintenance;

-- The INSERT names these ten columns, secret_ciphertext among them although a
-- decision email carries no secret, and returns the row's id. A second email
-- with the same dedupe key inserts nothing and reads the first row's id by
-- that key instead.
GRANT SELECT (id, dedupe_key),
      INSERT (id, to_user_id, to_address, template, locale, params, secret_ciphertext, dedupe_key, deliver_after, expires_at)
  ON app.email_outbox TO quizzivy_maintenance;

-- INSERT on the outbox is also what a leaked credential for this role would
-- use to mail any address in the centre's name. So a trigger that fires for
-- this role alone refuses, with 42501, any row that is not what the console
-- writes: a credit decision email (email.AICreditRequestDecided), with no
-- sealed secret, to an account at that account's own address. What the email
-- says is read again when it is sent (Mail.CreditDecision), so a row the role
-- forges can at most repeat a real decision to the teacher who asked.
-- SECURITY INVOKER, search_path pinned and EXECUTE revoked from PUBLIC, as
-- 00139's users guard is; every other role pays the WHEN test and nothing else.
-- +goose StatementBegin
CREATE FUNCTION app.email_outbox_maintenance_guard() RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog, pg_temp
AS $fn$
BEGIN
  IF NEW.template = 'ai_credit_request_decided'
     AND NEW.secret_ciphertext IS NULL
     AND NEW.to_address = (SELECT u.email FROM app.users u WHERE u.id = NEW.to_user_id) THEN
    RETURN NEW;
  END IF;
  RAISE EXCEPTION 'quizzivy_maintenance may only queue a credit decision email to its teacher''s own address'
    USING ERRCODE = 'insufficient_privilege';
END;
$fn$;
-- +goose StatementEnd

REVOKE ALL ON FUNCTION app.email_outbox_maintenance_guard() FROM PUBLIC;

CREATE TRIGGER email_outbox_maintenance_guard BEFORE INSERT ON app.email_outbox
  FOR EACH ROW WHEN (current_user = 'quizzivy_maintenance')
  EXECUTE FUNCTION app.email_outbox_maintenance_guard();

-- +goose Down

DROP TRIGGER IF EXISTS email_outbox_maintenance_guard ON app.email_outbox;
DROP FUNCTION IF EXISTS app.email_outbox_maintenance_guard();

REVOKE SELECT (id, dedupe_key),
       INSERT (id, to_user_id, to_address, template, locale, params, secret_ciphertext, dedupe_key, deliver_after, expires_at)
  ON app.email_outbox FROM quizzivy_maintenance;
REVOKE SELECT (locale) ON app.users FROM quizzivy_maintenance;
REVOKE SELECT (email) ON app.notification_preferences FROM quizzivy_maintenance;
