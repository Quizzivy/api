-- +goose Up

-- The owner check of retention-sweep's accounts step and anonymize-user
-- (retention.OwnsContent) counts question sets since T-R7.12, as it counts the
-- tests, questions, groups and media 00139 grants it: a teacher who owns only
-- a question set is skipped and reported like any other owner. The role reads
-- the owner column and nothing else of the table. What anonymisation deletes
-- from the messaging tables was granted with them (00163 to 00167). The grant
-- fails when the role is missing, as 00139 does.
GRANT SELECT (owner_id) ON app.question_sets TO quizzivy_maintenance;

-- +goose Down

REVOKE SELECT (owner_id) ON app.question_sets FROM quizzivy_maintenance;
