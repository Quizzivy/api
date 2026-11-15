-- +goose Up

-- Who is told of each new consultation request (T-R11.6): at most five email
-- addresses on the centre's settings row, written lower-case by the
-- application. Empty tells nobody. A column with a constant default rewrites
-- nothing and needs no backfill, and the table holds one row.
SET LOCAL lock_timeout = '5s';

ALTER TABLE app.organization_settings
  ADD COLUMN lead_recipients text[] NOT NULL DEFAULT '{}',
  ADD CONSTRAINT organization_settings_lead_recipients_check
    CHECK (cardinality(lead_recipients) <= 5);

-- +goose Down
SET LOCAL lock_timeout = '5s';

ALTER TABLE app.organization_settings
  DROP COLUMN lead_recipients;
