-- +goose Up

-- A question set is a named, ordered selection of its owner's standalone bank
-- questions (T-R7.1, DG-90 interim). It holds references, not copies, so an
-- edit to a question shows in every set that holds it, and a recipient of the
-- set sees what the owner puts in it, never what the owner tags later.
--
-- A set is soft-deleted, as the bank is, so quizzivy_app holds no DELETE on
-- it. Its items are hard rows: removing a question from a set deletes the row,
-- and the set's purge cascades to them. An item's question is RESTRICT, so a
-- hard purge of a question a set still holds is refused; the question's own
-- soft delete removes its items in the same transaction.
--
-- The foreign keys lock app.users and app.questions while the file runs, so
-- lock_timeout fails it at once rather than queue writers behind it.
SET LOCAL lock_timeout = '5s';

CREATE TABLE app.question_sets (
  id          uuid PRIMARY KEY DEFAULT uuidv7(),
  owner_id    uuid NOT NULL REFERENCES app.users(id) ON DELETE RESTRICT,
  name        text NOT NULL CHECK (length(btrim(name)) BETWEEN 1 AND 120),
  description text CHECK (length(description) <= 500),
  created_by  uuid NOT NULL REFERENCES app.users(id) ON DELETE RESTRICT,
  updated_by  uuid REFERENCES app.users(id) ON DELETE SET NULL,
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now(),
  deleted_at  timestamptz
);

-- The owner's list, most recently changed first.
CREATE INDEX question_sets_owner_idx ON app.question_sets (owner_id, updated_at DESC)
  WHERE deleted_at IS NULL;

CREATE TRIGGER question_sets_set_updated_at BEFORE UPDATE ON app.question_sets
  FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();

-- [D-13] The ordinal key is deferrable for a reorder inside one transaction.
CREATE TABLE app.question_set_items (
  set_id      uuid NOT NULL REFERENCES app.question_sets(id) ON DELETE CASCADE,
  question_id uuid NOT NULL REFERENCES app.questions(id) ON DELETE RESTRICT,
  ordinal     integer NOT NULL CHECK (ordinal >= 0),
  added_by    uuid NOT NULL REFERENCES app.users(id) ON DELETE RESTRICT,
  added_at    timestamptz NOT NULL DEFAULT now(),

  PRIMARY KEY (set_id, question_id),
  CONSTRAINT question_set_items_ordinal_key UNIQUE (set_id, ordinal)
    DEFERRABLE INITIALLY IMMEDIATE
);

-- Serves the RESTRICT key on a question's purge, the question's soft delete
-- removing its items, and the count of the sets that reference a question.
CREATE INDEX question_set_items_question_idx ON app.question_set_items (question_id);

-- quizzivy_app holds SELECT, INSERT, UPDATE and DELETE through the default
-- privileges of 00009. A set is deleted softly.
REVOKE DELETE ON app.question_sets FROM quizzivy_app;

-- +goose Down
SET LOCAL lock_timeout = '5s';

DROP TABLE app.question_set_items;
DROP TABLE app.question_sets;
