-- +goose Up

-- The addresses no mail goes to any more (T-R7.15, O-R5.2): one whose mail
-- hard-bounced, or whose owner marked it as spam. Resend's signed webhook
-- (POST /public/email-events) writes a row, and R5's dispatcher closes every
-- message to a listed address as failed with last_error_code 'SUPPRESSED'
-- instead of sending it. The address is stored lower-cased and trimmed, the
-- form the dispatcher compares the outbox's to_address in.
--
-- provider_event_id is the delivery's svix-id, so a replayed delivery inserts
-- nothing; so does a second event for an address already listed, which keeps
-- its first reason. A row is never changed. There is no foreign key: the list
-- is about an address, not an account, and changing a user's email clears
-- nothing, because the new address is not on it (O-R7.3).
--
-- A new, empty table with no foreign key: no other table is locked while the
-- file runs.
CREATE TABLE app.email_suppressions (
  address           text PRIMARY KEY,
  reason            text NOT NULL,
  provider_event_id text NOT NULL UNIQUE,
  created_at        timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT email_suppressions_address_check CHECK (address = lower(btrim(address))),
  CONSTRAINT email_suppressions_reason_check CHECK (reason IN ('bounced', 'complained'))
);

-- quizzivy_app holds SELECT, INSERT, UPDATE and DELETE through the default
-- privileges of 00009. The webhook inserts, the dispatcher reads, and DELETE
-- is what lifting a suppression will take; a row is never rewritten.
REVOKE UPDATE ON app.email_suppressions FROM quizzivy_app;

-- +goose Down
DROP TABLE app.email_suppressions;
