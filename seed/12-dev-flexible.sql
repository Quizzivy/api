-- Development seed, part twelve: a flexible assignment (F1, T-F1.9).
--
-- Relies on 01 (the Admin …00a1 and the class …00c1 with hocvien@quizzivy.com
-- in it) and 02's published test (…dd01, version …dd02: a single choice and a
-- short answer), so the frozen-question invariants in 99-assert.sql already
-- hold for it. Nothing else here reads a later seed.
--
-- One assignment, …00001201, for the live E2E of the flexible mode: start,
-- answer, take a break by the paper's ×, resume, leave the tab, hand in. Its
-- own row rather than one of 04's: the specs run in parallel, and two of them
-- on one assignment is a session takeover. The Admin owns the paper and
-- teaches the class, so the Admin is the teacher a flagged attempt notifies.
--
-- Open from an hour before the seed ran for thirty days after it, like 04's
-- and 10's. max_attempts is 50 for 04's reason: the suite runs again and
-- again against one database, with --repeat-each too.
--
-- Fullscreen is off, so headless Chromium is not stopped at the door, and
-- min_away_ms is the default 3000. The time limit and the leaving rule are
-- stored and not applied under flexible (00174), so they hold what strict
-- would act on at once: one minute, and the paper submitted on the second
-- counted absence. A flexible attempt that ever read them as strict would
-- time out or be submitted under the spec, which then fails loudly.
--
-- Fixed uuids, …00000000 12NN, ON CONFLICT DO NOTHING like every other seed.

INSERT INTO app.assignments
  (id, test_id, test_version_id, opens_at, closes_at, duration_minutes,
   max_attempts, mode, integrity_require_fullscreen, integrity_max_focus_loss,
   integrity_on_limit_exceeded, integrity_min_away_ms, published_at, created_by)
VALUES (
  '01935000-0000-7000-8000-000000001201'::uuid,
  '01935000-0000-7000-8000-00000000dd01'::uuid,
  '01935000-0000-7000-8000-00000000dd02'::uuid,
  now() - interval '1 hour',
  now() + interval '30 days',
  1,
  50,
  'flexible',
  false,
  1,
  'auto_submit',
  3000,
  now() - interval '1 hour',
  '01935000-0000-7000-8000-0000000000a1'
)
ON CONFLICT (id) DO NOTHING;

INSERT INTO app.assignment_classes (assignment_id, class_id)
VALUES ('01935000-0000-7000-8000-000000001201'::uuid, '01935000-0000-7000-8000-0000000000c1'::uuid)
ON CONFLICT DO NOTHING;
