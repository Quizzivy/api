-- +goose Up

-- The web host a session was opened at (spec §5.8): 'student' or 'staff' for
-- one opened from the student or the staff host's origin, and 'any' for one
-- opened from any other origin. Every token of a family carries its family's
-- audience, and a token is rotated only by a request of that audience. The
-- default is constant, so the column is added without rewriting the table, the
-- previous release's insert, which names no audience, keeps working, and every
-- session that already exists reads 'any': it was opened before the hosts were
-- split.
ALTER TABLE app.refresh_tokens
  ADD COLUMN audience text NOT NULL DEFAULT 'any'
    CONSTRAINT refresh_tokens_audience_check
    CHECK (audience IN ('any', 'student', 'staff'));

-- +goose Down

ALTER TABLE app.refresh_tokens
  DROP CONSTRAINT refresh_tokens_audience_check,
  DROP COLUMN audience;
