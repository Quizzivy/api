-- Development seed, part thirteen: the timetable (R8, T-R8.16), shaped like the Teacher deck's
-- CAL_EV, ATT_SES and ATT_RATE and the Student deck's AGENDA.
--
-- Relies on 06 (giaovien@quizzivy.com, Trần Thị Lan, …000000000601) and 08 (her four classes,
-- …0000008003NN, and their members, …0000008001NN and …0000008002NN). It adds no class, member
-- or account, so no messaging tie: 08 wrote those with the enrolments.
--
-- The term, the one the Teacher deck's Gradebook names:
--   Term 3      from the Monday eight weeks before the week the seed first ran, for eighteen
--               weeks: the current term, holding today, for ten weeks after that week
--
-- The weekly series, one per weekday as app.class_session_series holds them, each from its
-- first date in the term's first week to the term's last day (the end "Repeat every week"
-- gives a series that starts in a term), in Asia/Ho_Chi_Minh, meeting in the class's room
-- (the room a new session takes when the teacher names none):
--   IELTS 6.5 Evening    Mon · Wed · Fri   18:00   90 min   Room 3   …1311 …1312 …1313
--   IELTS Foundation A   Tue · Thu         17:30   90 min   Room 2   …1314 …1315
--   TOEIC 600 Weekend    Sat · Sun         09:00  120 min   Room 5   …1316 …1317
--   Kids Starters B      Sat               14:00   90 min   Room 1   …1318
-- 06's class and Minh's have none: CAL_EV draws these four only.
--
-- Attendance, taken by Lan at each session's end, for every occurrence of the term's first
-- eight weeks: 24 sessions of IELTS 6.5 Evening, 16 of Foundation A, 16 of TOEIC and 8 of
-- Kids. The current week (the ninth) is left untaken, so the Attendance page has sessions
-- that need a register and today's to take. In IELTS 6.5 Evening the deck's eight students
-- end the eight weeks on ATT_RATE exactly, late counting as attended and an unmarked session
-- left out, as the rate is read (T-R8.6):
--   Trần Minh Anh    24 / 24   100%       Đặng Quốc Huy    17 / 24   71%
--   Lê Hoàng Nam     22 / 24    92%       Bùi Ngọc Mai     21 / 24   88%
--   Nguyễn Gia Bảo   17 / 20    85%       Đỗ Phương Thảo   24 / 24  100%
--   Võ Khánh Linh    23 / 24    96%       Ngô Thanh Tùng   19 / 24   79%
-- Whole sessions over one count cannot give all eight: 85% needs 11 of 13 or 17 of 20, and
-- 96% at least 22 of 23. So Gia Bảo is left unmarked on four sessions of the third week
-- (Mon, Wed, Fri) and the fourth (Mon), which the rate leaves out. Everyone else in every
-- class is marked on every session: mostly present, some late, a few absent, by a hash of
-- the student and the date.
--
-- One per-date change, the Student deck's "Room 3 this week": IELTS Foundation A's Thursday
-- of the week the seed first ran moves from Room 2 to Room 3 with that note (…1321). The
-- deck draws it on IELTS 6.5 Evening's Friday, but 08 already gives that class Room 3; Gia
-- Bảo, the Student deck's student, is in both classes, so his This week shows it either way.
--
-- Everything after the term is read back from the stored rows, not from now(): a second run,
-- on any later day, finds the same series, dates and marks and writes nothing. A database
-- seeded more than ten weeks after its first seed has no current term and 99-assert refuses
-- it; reset the database and seed again. A term an admin added that overlaps this one stops
-- the seed the same way. Fixed ids …00000000 13NN; a stored session's id is derived from its
-- series and date. The whole file is one transaction.

BEGIN;

CREATE FUNCTION pg_temp.lan() RETURNS uuid
LANGUAGE sql IMMUTABLE AS $$ SELECT '01935000-0000-7000-8000-000000000601'::uuid $$;

CREATE FUNCTION pg_temp.cls(n int) RETURNS uuid
LANGUAGE sql IMMUTABLE AS $$ SELECT ('01935000-0000-7000-8000-0000008003' || lpad(to_hex(n), 2, '0'))::uuid $$;

CREATE FUNCTION pg_temp.named(n int) RETURNS uuid
LANGUAGE sql IMMUTABLE AS $$ SELECT ('01935000-0000-7000-8000-0000008001' || lpad(to_hex(n), 2, '0'))::uuid $$;

CREATE FUNCTION pg_temp.row_id(series uuid, on_date date) RETURNS uuid
LANGUAGE sql IMMUTABLE AS $$
  SELECT ('01935000-0000-7000-8000-' || substr(md5('13-dev-schedule:' || series || ':' || on_date), 1, 12))::uuid
$$;

-- -------------------------------------------------------------------- the term
INSERT INTO app.terms (id, name, starts_on, ends_on, created_by)
SELECT '01935000-0000-7000-8000-000000001301'::uuid, 'Term 3', w.monday - 56, w.monday - 56 + 125,
       '01935000-0000-7000-8000-0000000000a1'::uuid
  FROM (SELECT date_trunc('week', (now() AT TIME ZONE 'Asia/Ho_Chi_Minh')::date)::date AS monday) AS w
-- No target: an id that exists, or an admin's term that overlaps, leaves the row as it is.
ON CONFLICT DO NOTHING;

DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM app.terms WHERE id = '01935000-0000-7000-8000-000000001301') THEN
    RAISE EXCEPTION 'seed 13: Term 3 was not written: another term overlaps its weeks; delete it or reset the database';
  END IF;
END $$;

-- ------------------------------------------------------------------ the series
INSERT INTO app.class_session_series
  (id, class_id, weekday, start_time, duration_minutes, room, starts_on, ends_on, time_zone, created_by, created_at)
SELECT ('01935000-0000-7000-8000-0000000013' || v.n::text)::uuid, pg_temp.cls(v.class_n), v.weekday,
       v.start_time::time, v.minutes, c.room, t.starts_on + (v.weekday - 1), t.ends_on,
       'Asia/Ho_Chi_Minh', pg_temp.lan(), t.starts_on - 3
  FROM (VALUES
    (11, 1, 1, '18:00', 90),
    (12, 1, 3, '18:00', 90),
    (13, 1, 5, '18:00', 90),
    (14, 2, 2, '17:30', 90),
    (15, 2, 4, '17:30', 90),
    (16, 3, 6, '09:00', 120),
    (17, 3, 7, '09:00', 120),
    (18, 4, 6, '14:00', 90)
  ) AS v(n, class_n, weekday, start_time, minutes)
  JOIN app.classes c ON c.id = pg_temp.cls(v.class_n)
  CROSS JOIN app.terms t
 WHERE t.id = '01935000-0000-7000-8000-000000001301'
ON CONFLICT (id) DO NOTHING;

-- ---------------------------------------------------------- the marked sessions
-- An occurrence with attendance has a row, as T-R8.6's save writes it: the series' time and
-- length, no room or note of its own.
CREATE TEMP TABLE marked ON COMMIT DROP AS
SELECT s.id AS series_id, s.class_id, (s.starts_on + 7 * w)::date AS on_date,
       ((s.starts_on + 7 * w) + s.start_time) AT TIME ZONE s.time_zone AS starts_at,
       s.duration_minutes
  FROM app.class_session_series s
  CROSS JOIN generate_series(0, 7) AS w
 WHERE s.id::text LIKE '01935000-0000-7000-8000-00000000131_';

INSERT INTO app.class_sessions (id, class_id, series_id, series_date, starts_at, duration_minutes, created_by, created_at)
SELECT pg_temp.row_id(m.series_id, m.on_date), m.class_id, m.series_id, m.on_date, m.starts_at,
       m.duration_minutes, pg_temp.lan(), m.starts_at + make_interval(mins => m.duration_minutes)
  FROM marked m
ON CONFLICT (series_id, series_date) DO NOTHING;

-- The deck's eight in IELTS 6.5 Evening, by the session's place in the class's 24 (0 is the
-- first Monday): the sessions they missed, came late to, and were left unmarked on.
CREATE TEMP TABLE deck_eight ON COMMIT DROP AS
SELECT pg_temp.named(v.n) AS student_id, v.absent, v.late, v.unmarked
  FROM (VALUES
    (1,  '{}'::int[],                    '{}'::int[],       '{}'::int[]),          -- Trần Minh Anh   100
    (2,  '{5,17}',                       '{9,20}',          '{}'),                 -- Lê Hoàng Nam     92
    (4,  '{2,14,21}',                    '{18}',            '{6,7,8,9}'),          -- Nguyễn Gia Bảo   85
    (5,  '{13}',                         '{4}',             '{}'),                 -- Võ Khánh Linh    96
    (7,  '{1,4,8,11,15,19,22}',          '{6,13}',          '{}'),                 -- Đặng Quốc Huy    71
    (9,  '{7,16,23}',                    '{2}',             '{}'),                 -- Bùi Ngọc Mai     88
    (8,  '{}',                           '{}',              '{}'),                 -- Đỗ Phương Thảo  100
    (10, '{3,10,12,18,21}',              '{0,7,15}',        '{}')                  -- Ngô Thanh Tùng   79
  ) AS v(n, absent, late, unmarked);

INSERT INTO app.attendance_marks (session_id, student_id, status, marked_by, marked_at)
SELECT r.id, x.student_id, x.status, pg_temp.lan(), x.marked_at
  FROM (
    SELECT m.series_id, m.on_date, cm.user_id AS student_id,
           m.starts_at + make_interval(mins => m.duration_minutes) AS marked_at,
           CASE
             WHEN d.student_id IS NULL THEN
               CASE ('x' || substr(md5(cm.user_id::text || m.on_date::text), 1, 8))::bit(32)::int & 31
                 WHEN 0 THEN 'absent' WHEN 1 THEN 'absent' WHEN 2 THEN 'late' WHEN 3 THEN 'late'
                 ELSE 'present' END
             WHEN m.place = ANY (d.unmarked) THEN NULL
             WHEN m.place = ANY (d.absent) THEN 'absent'
             WHEN m.place = ANY (d.late) THEN 'late'
             ELSE 'present'
           END AS status
      FROM (SELECT *, (row_number() OVER (PARTITION BY class_id ORDER BY on_date) - 1)::int AS place FROM marked) AS m
      JOIN app.class_members cm ON cm.class_id = m.class_id
      LEFT JOIN deck_eight d ON d.student_id = cm.user_id AND m.class_id = pg_temp.cls(1)
  ) AS x
  JOIN app.class_sessions r ON r.series_id = x.series_id AND r.series_date = x.on_date
 WHERE x.status IS NOT NULL
ON CONFLICT (session_id, student_id) DO NOTHING;

-- ------------------------------------------------------------ the per-date change
INSERT INTO app.class_sessions (id, class_id, series_id, series_date, starts_at, duration_minutes, room, note, created_by)
SELECT '01935000-0000-7000-8000-000000001321'::uuid, s.class_id, s.id, s.starts_on + 56,
       ((s.starts_on + 56) + s.start_time) AT TIME ZONE s.time_zone, s.duration_minutes,
       'Room 3', 'Room 3 this week', pg_temp.lan()
  FROM app.class_session_series s
 WHERE s.id = '01935000-0000-7000-8000-000000001315'
ON CONFLICT DO NOTHING;

COMMIT;
