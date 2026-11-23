-- +goose Up

-- A term of the centre (T-R8.2, DG-93): a named run of dates, organisation-wide,
-- that admins keep under Admin › System settings (system.settings.write). The
-- attendance rate covers the term containing today, a weekly series without an
-- end runs to the end of the term containing its first date, and R9's
-- gradebook reads the same CurrentTerm.
--
-- Terms never overlap. The exclusion constraint holds that in the database, so
-- two admins saving at once cannot both win: T-R8.3 answers its 23P01 as
-- TERM_OVERLAPS without a pre-check. Both ends are inclusive ('[]'), so two
-- terms that share an end day overlap. A range type carries its own GiST
-- operator class, so the constraint needs no extension.
--
-- created_by is RESTRICT, as every authored row's is: deleteUser answers 409
-- with referencedBy owned_content.
--
-- The foreign key locks app.users while the file runs, so lock_timeout fails
-- it at once rather than queue writers behind it.
SET LOCAL lock_timeout = '5s';

CREATE TABLE app.terms (
  id         uuid PRIMARY KEY DEFAULT uuidv7(),
  name       text NOT NULL CHECK (length(btrim(name)) BETWEEN 1 AND 60),
  starts_on  date NOT NULL,
  ends_on    date NOT NULL,
  created_by uuid NOT NULL REFERENCES app.users(id) ON DELETE RESTRICT,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT terms_ends_on_check CHECK (ends_on >= starts_on),
  CONSTRAINT terms_no_overlap EXCLUDE USING gist (daterange(starts_on, ends_on, '[]') WITH &&)
);

CREATE TRIGGER terms_set_updated_at BEFORE UPDATE ON app.terms
  FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();

-- quizzivy_app holds SELECT, INSERT, UPDATE and DELETE through the default
-- privileges of 00009: an admin creates, edits and deletes a term.

-- +goose Down
SET LOCAL lock_timeout = '5s';

DROP TABLE app.terms;
