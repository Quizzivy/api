-- +goose Up

-- A grant of one teacher's test or question set to another, as Can use or Can
-- edit (T-R7.1, D4). The row stores no owner: the owner is always the
-- resource's current owner, reached through the resource's own foreign key,
-- so a grant cannot disagree with its resource and an ownership transfer moves
-- every grant without a write. Each kind has its own nullable foreign key,
-- named by kind, rather than a polymorphic resource id (D-05, D-17).
--
-- The two UNIQUE keys bind one kind each, because NULLs are distinct, and each
-- serves the owner's lookup of a resource's grants. The resource keys cascade:
-- a purged resource takes its grants. A soft-deleted one keeps them until the
-- delete removes them itself.
--
-- R10 (T-R10.10) widens the table for word lists and courses:
--   - add word_list_id uuid REFERENCES app.word_lists ON DELETE CASCADE and
--     course_id uuid REFERENCES app.courses ON DELETE CASCADE, each foreign key
--     NOT VALID then VALIDATE; no owner column and no composite foreign key
--   - replace content_shares_one_resource with
--     num_nonnulls(test_id, question_set_id, word_list_id, course_id) = 1
--   - replace content_shares_kind_check with
--     kind IN ('test','question_set','word_list','course')
--   - replace content_shares_kind_matches with one equality per kind
--   - each replacement CHECK NOT VALID then VALIDATE
--
-- The foreign keys lock app.users, app.tests and app.question_sets while the
-- file runs, so lock_timeout fails it at once rather than queue writers.
SET LOCAL lock_timeout = '5s';

CREATE TABLE app.content_shares (
  id              uuid PRIMARY KEY DEFAULT uuidv7(),
  kind            text NOT NULL,
  test_id         uuid REFERENCES app.tests(id) ON DELETE CASCADE,
  question_set_id uuid REFERENCES app.question_sets(id) ON DELETE CASCADE,
  grantee_id      uuid NOT NULL REFERENCES app.users(id) ON DELETE CASCADE,
  access          text NOT NULL CHECK (access IN ('use', 'edit')),
  granted_by      uuid NOT NULL REFERENCES app.users(id) ON DELETE RESTRICT,
  message         text CHECK (length(message) BETWEEN 1 AND 500),
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),

  CONSTRAINT content_shares_kind_check CHECK (kind IN ('test', 'question_set')),
  CONSTRAINT content_shares_one_resource CHECK (num_nonnulls(test_id, question_set_id) = 1),
  CONSTRAINT content_shares_kind_matches CHECK ((kind = 'test') = (test_id IS NOT NULL)),
  CONSTRAINT content_shares_test_grantee_key UNIQUE (test_id, grantee_id),
  CONSTRAINT content_shares_question_set_grantee_key UNIQUE (question_set_id, grantee_id)
);

-- Shared with me, newest first; also serves the cascade from app.users.
CREATE INDEX content_shares_grantee_idx ON app.content_shares (grantee_id, created_at DESC);

CREATE TRIGGER content_shares_set_updated_at BEFORE UPDATE ON app.content_shares
  FOR EACH ROW EXECUTE FUNCTION app.set_updated_at();

-- quizzivy_app holds SELECT, INSERT, UPDATE and DELETE through the default
-- privileges of 00009: revoking a grant deletes its row.

-- +goose Down
SET LOCAL lock_timeout = '5s';

DROP TABLE app.content_shares;
