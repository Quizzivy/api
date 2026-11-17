-- +goose Up

-- T-R7.13: the switches saved before the email channel did not choose email.
-- Until R7 nothing was emailed, the settings page showed the In app switch
-- alone, and it saved every row with the email the API answered for an
-- unsaved switch, which was false. Since T-R7.13 a staff member's email
-- column decides their email, so a teacher who once adjusted their
-- notifications would get no flagged or message emails, while one who never
-- did gets them by the deck's default. This gives those rows the deck's
-- default: on for a staff member's attempt.flagged, message.received,
-- summary.weekly, assignment.due_soon and result.ready
-- (domain.DefaultPreference). A row that holds true was set through the API
-- and is kept, and the off defaults (attempt.submitted, assignment.closing)
-- are false already. A student's email is their in-app switch (DG-82), which
-- the column is stored as from now on (Recipient.Keep), so their rows are
-- brought into line too; nothing reads a student's column.
SET LOCAL lock_timeout = '5s';

UPDATE app.notification_preferences p
   SET email = true
  FROM app.users u
 WHERE u.id = p.user_id
   AND u.role_id NOT IN (SELECT r.id FROM app.student_like_roles r)
   AND NOT p.email
   AND p.event IN ('attempt.flagged', 'message.received', 'summary.weekly', 'assignment.due_soon', 'result.ready');

UPDATE app.notification_preferences p
   SET email = p.in_app
  FROM app.users u
 WHERE u.id = p.user_id
   AND u.role_id IN (SELECT r.id FROM app.student_like_roles r)
   AND p.email <> p.in_app;

-- +goose Down
-- Nothing to undo. The code before T-R7.13 never reads the email column, so
-- the values Up wrote do not change what it does, and after Up they cannot be
-- told apart from a choice a teacher made since. Writing false back would turn
-- off the flagged and message emails of every teacher who saved a switch,
-- which is the fault Up repairs.
SELECT 1;
