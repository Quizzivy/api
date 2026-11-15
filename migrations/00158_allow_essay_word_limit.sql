-- +goose Up

-- An essay's word limit (the owner, 2026-11-15). It is the same column and the
-- same rule as a short answer's (00147): advice, never truncation, NULL is no
-- limit, the frozen copy carries it. A short answer keeps 1 to 100; an essay
-- takes 1 to 1000. Every type but the two still has none.
SET LOCAL lock_timeout = '5s';

ALTER TABLE app.questions DROP CONSTRAINT questions_max_words_check;
ALTER TABLE app.questions
  ADD CONSTRAINT questions_max_words_check
    CHECK (max_words IS NULL
           OR (type = 'short_answer' AND max_words BETWEEN 1 AND 100)
           OR (type = 'essay' AND max_words BETWEEN 1 AND 1000))
    NOT VALID;
ALTER TABLE app.questions VALIDATE CONSTRAINT questions_max_words_check;

ALTER TABLE app.test_version_questions DROP CONSTRAINT test_version_questions_max_words_check;
ALTER TABLE app.test_version_questions
  ADD CONSTRAINT test_version_questions_max_words_check
    CHECK (max_words IS NULL
           OR (type = 'short_answer' AND max_words BETWEEN 1 AND 100)
           OR (type = 'essay' AND max_words BETWEEN 1 AND 1000))
    NOT VALID;
ALTER TABLE app.test_version_questions VALIDATE CONSTRAINT test_version_questions_max_words_check;

-- +goose Down

SET LOCAL lock_timeout = '5s';

-- Refuses while an essay, in the bank or frozen in a version, has a word
-- limit: the old rule cannot hold it, and dropping the limit would change
-- what its students were told.
-- +goose StatementBegin
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM app.questions WHERE type = 'essay' AND max_words IS NOT NULL)
     OR EXISTS (SELECT 1 FROM app.test_version_questions WHERE type = 'essay' AND max_words IS NOT NULL) THEN
    RAISE EXCEPTION 'an essay with a word limit exists; the short-answer-only rule cannot hold it';
  END IF;
END
$$;
-- +goose StatementEnd

ALTER TABLE app.test_version_questions DROP CONSTRAINT test_version_questions_max_words_check;
ALTER TABLE app.test_version_questions
  ADD CONSTRAINT test_version_questions_max_words_check
    CHECK (max_words IS NULL OR (type = 'short_answer' AND max_words BETWEEN 1 AND 100));

ALTER TABLE app.questions DROP CONSTRAINT questions_max_words_check;
ALTER TABLE app.questions
  ADD CONSTRAINT questions_max_words_check
    CHECK (max_words IS NULL OR (type = 'short_answer' AND max_words BETWEEN 1 AND 100));
