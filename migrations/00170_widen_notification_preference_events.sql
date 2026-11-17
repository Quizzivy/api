-- +goose Up

-- T-R7.13's widen_notification_preference_events: the seventh switch,
-- summary.weekly (the teacher's Monday summary, T-R7.14), which the email
-- contract (sdk#29) adds to NotificationEvent. 00162 (T-R7.9) already added
-- message.received to R4's five keys, so this file states the whole list the
-- plan gives and its Down returns to 00162's list; 00162's own Down deletes
-- the message.received rows and restores R4's five keys. Applied together,
-- the two files are the plan's one change. R10's T-R10.6 restores exactly
-- this list on its Down:
--   ('attempt.submitted','attempt.flagged','assignment.closing','assignment.due_soon','result.ready','message.received','summary.weekly')
--
-- Replaced under R4's name, NOT VALID then VALIDATE, as 00162 does: the
-- validation scans the table under SHARE UPDATE EXCLUSIVE, which writers do
-- not wait for, and lock_timeout fails the file at once rather than queue
-- writers behind the ACCESS EXCLUSIVE the ALTER takes.
SET LOCAL lock_timeout = '5s';

ALTER TABLE app.notification_preferences DROP CONSTRAINT notification_preferences_event_check;
ALTER TABLE app.notification_preferences
  ADD CONSTRAINT notification_preferences_event_check CHECK (event IN (
    'attempt.submitted', 'attempt.flagged', 'assignment.closing', 'assignment.due_soon', 'result.ready',
    'message.received', 'summary.weekly')) NOT VALID;
ALTER TABLE app.notification_preferences VALIDATE CONSTRAINT notification_preferences_event_check;

-- +goose Down
SET LOCAL lock_timeout = '5s';

-- A saved summary.weekly switch is a reconstructible default: without the row
-- the switch reads as the deck's default again (off in the app, on by email).
DELETE FROM app.notification_preferences WHERE event = 'summary.weekly';
ALTER TABLE app.notification_preferences DROP CONSTRAINT notification_preferences_event_check;
ALTER TABLE app.notification_preferences
  ADD CONSTRAINT notification_preferences_event_check CHECK (event IN (
    'attempt.submitted', 'attempt.flagged', 'assignment.closing', 'assignment.due_soon', 'result.ready',
    'message.received')) NOT VALID;
ALTER TABLE app.notification_preferences VALIDATE CONSTRAINT notification_preferences_event_check;
