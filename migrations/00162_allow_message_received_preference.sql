-- +goose Up

-- The sixth switch, message.received ("New message" for a teacher, "Messages
-- from teachers" for a student), which the messaging contract (sdk#28) adds to
-- NotificationEvent (T-R7.9). GET and PUT /me/notification-preferences answer
-- and store every switch, so the R4 CHECK (00081) has to admit it before the
-- binary that serves six switches does. T-R7.13's
-- widen_notification_preference_events adds summary.weekly on top of this list.
--
-- Replaced under R4's name, NOT VALID then VALIDATE: the validation scans the
-- table under SHARE UPDATE EXCLUSIVE, which writers do not wait for, and
-- lock_timeout fails the file at once rather than queue writers behind the
-- ACCESS EXCLUSIVE the ALTER takes.
SET LOCAL lock_timeout = '5s';

ALTER TABLE app.notification_preferences DROP CONSTRAINT notification_preferences_event_check;
ALTER TABLE app.notification_preferences
  ADD CONSTRAINT notification_preferences_event_check CHECK (event IN (
    'attempt.submitted', 'attempt.flagged', 'assignment.closing', 'assignment.due_soon', 'result.ready',
    'message.received')) NOT VALID;
ALTER TABLE app.notification_preferences VALIDATE CONSTRAINT notification_preferences_event_check;

-- +goose Down
SET LOCAL lock_timeout = '5s';

-- A saved message.received switch is a reconstructible default: without the
-- row the switch reads as on in the app, as it did before this file.
DELETE FROM app.notification_preferences WHERE event = 'message.received';
ALTER TABLE app.notification_preferences DROP CONSTRAINT notification_preferences_event_check;
ALTER TABLE app.notification_preferences
  ADD CONSTRAINT notification_preferences_event_check CHECK (event IN (
    'attempt.submitted', 'attempt.flagged', 'assignment.closing', 'assignment.due_soon', 'result.ready')) NOT VALID;
ALTER TABLE app.notification_preferences VALIDATE CONSTRAINT notification_preferences_event_check;
