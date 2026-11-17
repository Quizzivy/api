-- Development seed, part eleven: sharing between teachers and Messages (R7, T-R7.24).
--
-- Relies on 06 (giaovien@quizzivy.com, Trần Thị Lan, and giaovien2@quizzivy.com, Lê Văn
-- Minh, R2's second teacher, with his class and his draft "Farm animals quiz"), 08 (Lan's workspace: her tests, bank, classes, roster and
-- Lê Hoàng Nam's flagged attempt on the Reading Mock) and 09 (the Teachers Ngô Thu Trang
-- and Đinh Quốc Bảo). Roles and the messaging triggers come from migrations.
--
-- Sharing, the Teacher deck's SHARE_IN and SHARE_0 with Minh in Phan Minh Đức's place and
-- Ngô Thu Trang in Nguyễn Thu Trang's:
--   Shared with Lan    IELTS Writing Task 2 · Mock set   Minh's test, Can edit
--                      Collocations                      Minh's question set, Can use
--                      Kids Starters · Animals quiz      Trang's test, Can edit
--   Lan shares         Mid-term Reading Mock             with Minh, Can edit
--                      Vocabulary Quiz: Travel           with Trang and Bảo, Can use
--                      Travel vocabulary                 her question set, with Minh, Can use
-- Word lists and courses (SHARE_IN's s3 and s4) arrive with R10. Collocations holds 12
-- questions where the deck's card reads 48, so Lan's card reads 12.
--
-- Minh also owns "Farm animals · Unit check", published and shared with no one, which holds
-- 06's question "Write three animals that live on a farm." that his draft "Farm animals
-- quiz" holds too: a co-editor's edit of that question forks it (T-R7.6). It is the
-- round trip sharing.live.spec.ts runs.
--
-- Messages, the Teacher deck's CONVS and MSGS for Lan, with the reads that give its unread
-- counts and "Seen by" figures. The words are the Vietnamese of the R7 Messages proposal's
-- SAY table where it has them, and a translation of the deck's English where it does not
-- (Thu Hà's thread, and the Foundation A and TOEIC posts):
--   Lê Hoàng Nam        two unread, after his flagged Reading Mock attempt (the context card)
--   Phạm Thu Hà         one unread, after Lan's reply
--   Đỗ Phương Thảo      read
--   IELTS 6.5 Evening   two announcements, seen by 22 and 21 of 24 (the deck's 24 of 24
--                       counts two students who have never signed in, so no read is theirs)
--   IELTS Foundation A  one announcement, seen by 13 of 16
--   TOEIC 600 Weekend   one announcement, seen by 20 of 22
-- and the Student deck's SCONVS for Nguyễn Gia Bảo: Lan's two messages, the second unread,
-- and the IELTS 6.5 Evening and Foundation A announcements, read. The deck's times are
-- kept relative to Nam's attempt, which stands for its 20:14.
--
-- And for the live specs: Đào Minh Khang (khang.dao@quizzivy.com), a student Minh created,
-- is the only member of Minh's class, so an announcement there reads "Seen by 1 of 1" once
-- he opens it; Minh has posted one, and the two have a direct thread. Nothing is unread on
-- either side. He is in no class of the Admin's and no peer of the Admin's, so the
-- Admin's token finds no thread with him: hocvien@quizzivy.com could not serve, being in
-- the Admin's class.
--
-- Written as the API writes it: a grant names no owner, a set holds its owner's standalone
-- questions, a thread is opened by its first post, a message's id is a uuidv7 of the
-- moment it was sent (pointers, unread counts and "Seen by" compare ids), the thread's
-- last_message_at is its newest post, and messages are only inserted. No audit or
-- notification rows: 08 writes none for what it publishes either. No file is attached: the
-- seed has no object store (README.md, "Media objects").
--
-- Ids are fixed: pg_temp.sid(kind, n) is …00000110 + kind + n, pg_temp.did(label) derives
-- the rest from a label. Kinds: 1 tests, 2 questions, 3 question sets, 4 grants,
-- 5 conversations, 6 users. A message is found again by its sender and client id, which
-- did makes. Idempotent like the other seeds: a row that exists is left as it is. One transaction.

BEGIN;

CREATE FUNCTION pg_temp.sid(kind int, n int) RETURNS uuid
LANGUAGE sql IMMUTABLE AS $$
  SELECT ('01935000-0000-7000-8000-00000110' || lpad(to_hex(kind), 2, '0') || lpad(to_hex(n), 2, '0'))::uuid
$$;

CREATE FUNCTION pg_temp.did(label text) RETURNS uuid
LANGUAGE sql IMMUTABLE AS $$
  SELECT ('01935000-0000-7000-8000-' || substr(md5('11-dev-collaboration:' || label), 1, 12))::uuid
$$;

-- 08's ids (its pg_temp.sid): 1 named students, 3 classes, 5 tests, 6 bank questions,
-- 7 assignments.
CREATE FUNCTION pg_temp.ws(kind int, n int) RETURNS uuid
LANGUAGE sql IMMUTABLE AS $$
  SELECT ('01935000-0000-7000-8000-00000080' || lpad(to_hex(kind), 2, '0') || lpad(to_hex(n), 2, '0'))::uuid
$$;

CREATE FUNCTION pg_temp.lan() RETURNS uuid LANGUAGE sql IMMUTABLE AS $$ SELECT '01935000-0000-7000-8000-000000000601'::uuid $$;
CREATE FUNCTION pg_temp.minh() RETURNS uuid LANGUAGE sql IMMUTABLE AS $$ SELECT '01935000-0000-7000-8000-000000000602'::uuid $$;
CREATE FUNCTION pg_temp.trang() RETURNS uuid LANGUAGE sql IMMUTABLE AS $$ SELECT '01935000-0000-7000-8000-000000900015'::uuid $$;
CREATE FUNCTION pg_temp.bao() RETURNS uuid LANGUAGE sql IMMUTABLE AS $$ SELECT '01935000-0000-7000-8000-000000900014'::uuid $$;
CREATE FUNCTION pg_temp.khang() RETURNS uuid LANGUAGE sql IMMUTABLE AS $$ SELECT pg_temp.sid(6, 1) $$;
CREATE FUNCTION pg_temp.minh_class() RETURNS uuid LANGUAGE sql IMMUTABLE AS $$ SELECT '01935000-0000-7000-8000-000000000621'::uuid $$;
CREATE FUNCTION pg_temp.farm_question() RETURNS uuid LANGUAGE sql IMMUTABLE AS $$ SELECT '01935000-0000-7000-8000-000000000624'::uuid $$;

-- ---------------------------------------------------------------- questions
-- put_question writes one standalone bank question of owner with its options: type,
-- prompt, points, level, skill, tags, options, correct (1-based positions), sample and
-- ageDays. A question that exists is left as its owner has it.
CREATE FUNCTION pg_temp.put_question(n int, owner uuid, spec jsonb) RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
  qid uuid := pg_temp.sid(2, n);
  opts text[] := ARRAY(SELECT jsonb_array_elements_text(coalesce(spec->'options', '[]'::jsonb)));
  correct int[] := ARRAY(SELECT jsonb_array_elements_text(coalesce(spec->'correct', '[]'::jsonb))::int);
  age interval := make_interval(days => coalesce((spec->>'ageDays')::int, 20));
  inserted int;
BEGIN
  INSERT INTO app.questions
    (id, type, prompt, points, sample_answer, tags, created_by, owner_id, created_at, updated_at, level, skill)
  VALUES (qid, (spec->>'type')::app.question_type, spec->>'prompt', (spec->>'points')::numeric, spec->>'sample',
          ARRAY(SELECT jsonb_array_elements_text(coalesce(spec->'tags', '[]'::jsonb))),
          owner, owner, now() - age, now() - age, spec->>'level', spec->>'skill')
  ON CONFLICT (id) DO NOTHING;
  GET DIAGNOSTICS inserted = ROW_COUNT;
  IF inserted = 0 THEN
    RETURN;
  END IF;
  FOR i IN 1..coalesce(array_length(opts, 1), 0) LOOP
    INSERT INTO app.question_options (id, question_id, ordinal, text, is_correct)
    VALUES (pg_temp.did('qo:' || qid || ':' || i), qid, i - 1, opts[i], i = ANY (correct));
  END LOOP;
END;
$$;

-- Minh: IELTS Writing Task 2 · Mock set (1, 2), Farm animals · Unit check (3, with 06's
-- farm question) and Collocations (16 to 27).
SELECT pg_temp.put_question(1, pg_temp.minh(), $q$ {"type":"short_answer","points":9,"level":"b2","skill":"writing","tags":["writing","task 2"],"ageDays":21,
  "prompt":"Some people believe that children should start learning a foreign language at primary school. Others think it is better to wait until secondary school. Discuss both views and give your own opinion. Write at least 250 words."} $q$);
SELECT pg_temp.put_question(2, pg_temp.minh(), $q$ {"type":"short_answer","points":9,"level":"b2","skill":"writing","tags":["writing","task 2"],"ageDays":21,
  "prompt":"In many cities, traffic congestion is getting worse. What are the causes of this problem, and what measures could be taken to solve it? Write at least 250 words."} $q$);
SELECT pg_temp.put_question(3, pg_temp.minh(), $q$ {"type":"single_choice","points":1,"level":"a1","skill":"vocabulary","tags":["vocabulary","animals"],"ageDays":14,
  "prompt":"Which animal gives us wool?","options":["Sheep","Cow","Hen","Goat"],"correct":[1]} $q$);

SELECT pg_temp.put_question(15 + v.n, pg_temp.minh(), jsonb_build_object(
         'type', 'single_choice', 'points', 1, 'level', 'b1', 'skill', 'vocabulary', 'tags', jsonb_build_array('vocabulary', 'collocations'),
         'ageDays', 30, 'prompt', 'Choose the word that completes the collocation: ' || v.prompt,
         'options', v.options, 'correct', jsonb_build_array(1)))
  FROM (VALUES
    (1,  '___ a decision',                      '["make","do","get","say"]'::jsonb),
    (2,  '___ your homework',                   '["do","make","take","get"]'::jsonb),
    (3,  '___ a photo',                         '["take","make","do","bring"]'::jsonb),
    (4,  '___ a mistake',                       '["make","do","take","say"]'::jsonb),
    (5,  '___ attention to the teacher',        '["pay","give","make","take"]'::jsonb),
    (6,  '___ the truth',                       '["tell","say","speak","talk"]'::jsonb),
    (7,  '___ a promise you made',              '["keep","hold","save","stay"]'::jsonb),
    (8,  '___ fun at the party',                '["have","make","do","take"]'::jsonb),
    (9,  '___ time by taking the metro',        '["save","keep","earn","spend"]'::jsonb),
    (10, 'heavy ___ all night',                 '["rain","sun","sky","cloud"]'::jsonb),
    (11, '___ a break after lunch',             '["take","do","put","bring"]'::jsonb),
    (12, '___ an effort to arrive on time',     '["make","do","get","put"]'::jsonb)
  ) AS v(n, prompt, options);

-- Trang: Kids Starters · Animals quiz (4 to 6).
SELECT pg_temp.put_question(4, pg_temp.trang(), $q$ {"type":"single_choice","points":1,"level":"a1","skill":"vocabulary","tags":["vocabulary","animals","kids"],"ageDays":20,
  "prompt":"Which animal says “moo”?","options":["Cow","Dog","Cat","Duck"],"correct":[1]} $q$);
SELECT pg_temp.put_question(5, pg_temp.trang(), $q$ {"type":"single_choice","points":1,"level":"a1","skill":"vocabulary","tags":["vocabulary","animals","kids"],"ageDays":20,
  "prompt":"Which animal can fly?","options":["Bird","Fish","Horse","Pig"],"correct":[1]} $q$);
SELECT pg_temp.put_question(6, pg_temp.trang(), $q$ {"type":"short_answer","points":1,"level":"a1","skill":"writing","tags":["animals","kids"],"ageDays":20,
  "prompt":"Write the name of an animal that lives in the sea.","sample":"fish"} $q$);

-- -------------------------------------------------------------------- tests
INSERT INTO app.tests (id, title, description, status, current_version, created_by, owner_id, created_at, updated_at)
SELECT pg_temp.sid(1, v.n), v.title, v.description, 'published', 1, v.owner, v.owner, now() - v.created, now() - v.edited
  FROM (VALUES
    (1, 'IELTS Writing Task 2 · Mock set', 'Hai đề Writing Task 2 thi thử, chấm theo band.', pg_temp.minh(),  interval '21 days', interval '3 days'),
    (2, 'Farm animals · Unit check',       'Bài kiểm tra cuối bài về động vật ở trang trại.', pg_temp.minh(), interval '14 days', interval '5 days'),
    (3, 'Kids Starters · Animals quiz',    'Câu hỏi con vật cho lớp Starters.',              pg_temp.trang(), interval '20 days', interval '12 days')
  ) AS v(n, title, description, owner, created, edited)
ON CONFLICT (id) DO NOTHING;

-- put_section writes a test's section if its id and its place are free and puts the
-- questions in it in order; a question already there, or an ordinal taken, is skipped.
CREATE FUNCTION pg_temp.put_section(test_n int, place int, heading text, intro text, members uuid[])
RETURNS void LANGUAGE plpgsql AS $$
DECLARE
  section_id uuid := pg_temp.did('section:' || test_n || ':' || place);
BEGIN
  INSERT INTO app.test_sections (id, test_id, ordinal, title, instructions)
  SELECT section_id, pg_temp.sid(1, test_n), place, heading, intro
   WHERE NOT EXISTS (SELECT 1 FROM app.test_sections s
                      WHERE s.id = section_id OR (s.test_id = pg_temp.sid(1, test_n) AND s.ordinal = place));
  FOR i IN 1..coalesce(array_length(members, 1), 0) LOOP
    INSERT INTO app.test_section_questions (id, test_section_id, ordinal, question_id)
    SELECT pg_temp.did('member:' || section_id || ':' || members[i]), section_id, i - 1, members[i]
     WHERE EXISTS (SELECT 1 FROM app.test_sections s WHERE s.id = section_id)
       AND NOT EXISTS (SELECT 1 FROM app.test_section_questions m
                        WHERE m.test_section_id = section_id AND (m.question_id = members[i] OR m.ordinal = i - 1))
       AND NOT EXISTS (SELECT 1 FROM app.test_section_units u WHERE u.test_section_id = section_id);
  END LOOP;
END;
$$;

-- freeze snapshots a test's draft as version 1, as publish does (08's freeze, by the
-- test's owner), unless the test has a version: the sections and each question with its
-- options. These tests hold choice and short-answer questions only.
CREATE FUNCTION pg_temp.freeze(test_n int, note text, days_ago int) RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
  tid uuid := pg_temp.sid(1, test_n);
  vid uuid := pg_temp.did('version:' || test_n || ':1');
BEGIN
  IF EXISTS (SELECT 1 FROM app.test_versions WHERE test_id = tid) THEN
    RETURN;
  END IF;

  INSERT INTO app.test_versions (id, test_id, version, total_points, published_at, published_by, delivery_version, change_note)
  SELECT vid, tid, 1, sum(q.points), now() - days_ago * interval '1 day', (SELECT owner_id FROM app.tests WHERE id = tid), 'group_v1', note
    FROM app.test_sections s
    JOIN app.test_section_questions m ON m.test_section_id = s.id
    JOIN app.questions q ON q.id = m.question_id
   WHERE s.test_id = tid;

  INSERT INTO app.test_version_sections (id, test_version_id, ordinal, title, instructions)
  SELECT pg_temp.did('vsection:' || vid || ':' || s.ordinal), vid, s.ordinal, s.title, s.instructions
    FROM app.test_sections s WHERE s.test_id = tid;

  INSERT INTO app.test_version_questions
    (id, test_version_section_id, ordinal, source_question_id, type, prompt, points, explanation, sample_answer,
     prompt_content, explanation_content, level, skill)
  SELECT pg_temp.did('vq:' || vid || ':' || q.id), pg_temp.did('vsection:' || vid || ':' || s.ordinal), m.ordinal, q.id,
         q.type, q.prompt, q.points, q.explanation, q.sample_answer, q.prompt_content, q.explanation_content, q.level, q.skill
    FROM app.test_sections s
    JOIN app.test_section_questions m ON m.test_section_id = s.id
    JOIN app.questions q ON q.id = m.question_id
   WHERE s.test_id = tid;

  INSERT INTO app.test_version_options (id, test_version_question_id, ordinal, text, is_correct, content)
  SELECT pg_temp.did('vo:' || f.id || ':' || o.ordinal), f.id, o.ordinal, o.text, o.is_correct, o.content
    FROM app.test_version_questions f
    JOIN app.test_version_sections vs ON vs.id = f.test_version_section_id AND vs.test_version_id = vid
    JOIN app.question_options o ON o.question_id = f.source_question_id;
END;
$$;

SELECT pg_temp.put_section(1, 0, 'Writing Task 2', 'Write at least 250 words in 40 minutes.', ARRAY[pg_temp.sid(2, 1), pg_temp.sid(2, 2)]);
SELECT pg_temp.freeze(1, 'Bản đầu tiên: hai đề.', 3);

SELECT pg_temp.put_section(2, 0, 'Phần 1', NULL, ARRAY[pg_temp.farm_question(), pg_temp.sid(2, 3)]);
SELECT pg_temp.freeze(2, 'Bản đầu tiên.', 5);

SELECT pg_temp.put_section(3, 0, 'Animals', NULL, ARRAY[pg_temp.sid(2, 4), pg_temp.sid(2, 5), pg_temp.sid(2, 6)]);
SELECT pg_temp.freeze(3, 'Bản đầu tiên.', 12);

-- ------------------------------------------------------------ question sets
-- A set holds references to its owner's standalone bank questions, in order. Its items
-- are written while it has none: the owner may have reordered or removed them since, and
-- the ordinal key is deferrable, which ON CONFLICT cannot take as an arbiter.
INSERT INTO app.question_sets (id, owner_id, name, description, created_by, updated_by, created_at, updated_at)
VALUES
  (pg_temp.sid(3, 1), pg_temp.minh(), 'Collocations', 'Common verb and noun collocations for B1.',
   pg_temp.minh(), pg_temp.minh(), now() - interval '25 days', now() - interval '25 days'),
  (pg_temp.sid(3, 2), pg_temp.lan(), 'Travel vocabulary', 'The travel words of the Vocabulary Quiz.',
   pg_temp.lan(), pg_temp.lan(), now() - interval '3 days', now() - interval '3 days')
ON CONFLICT (id) DO NOTHING;

INSERT INTO app.question_set_items (set_id, question_id, ordinal, added_by, added_at)
SELECT i.set_id, i.question_id, i.ordinal, i.added_by, i.added_at
  FROM (SELECT pg_temp.sid(3, 1), pg_temp.sid(2, 15 + n), n - 1, pg_temp.minh(), now() - interval '25 days'
          FROM generate_series(1, 12) AS n
        UNION ALL
        SELECT pg_temp.sid(3, 2), pg_temp.ws(6, 9 + n), n - 1, pg_temp.lan(), now() - interval '3 days'
          FROM generate_series(1, 6) AS n) AS i(set_id, question_id, ordinal, added_by, added_at)
 WHERE EXISTS (SELECT 1 FROM app.question_sets s WHERE s.id = i.set_id AND s.deleted_at IS NULL)
   AND NOT EXISTS (SELECT 1 FROM app.question_set_items held WHERE held.set_id = i.set_id)
ON CONFLICT (set_id, question_id) DO NOTHING;

-- ------------------------------------------------------------------- grants
-- No owner is stored: the owner is the resource's. granted_by is its owner here.
INSERT INTO app.content_shares (id, kind, test_id, question_set_id, grantee_id, access, granted_by, message, created_at, updated_at)
SELECT pg_temp.sid(4, v.n), v.kind, v.test_id, v.set_id, v.grantee, v.access, v.by, v.message, now() - v.ago, now() - v.ago
  FROM (VALUES
    -- Shared with Lan (SHARE_IN): s1, s2 and s5.
    (1, 'test',         pg_temp.sid(1, 1), NULL::uuid,        pg_temp.lan(),   'edit', pg_temp.minh(),  'Cô Lan xem giúp em đề thứ hai nhé, em chưa chắc phần gợi ý.', interval '6 hours'),
    (2, 'question_set', NULL::uuid,        pg_temp.sid(3, 1), pg_temp.lan(),   'use',  pg_temp.minh(),  NULL, interval '1 day 2 hours'),
    (3, 'test',         pg_temp.sid(1, 3), NULL::uuid,        pg_temp.lan(),   'edit', pg_temp.trang(), NULL, interval '9 days'),
    -- Lan's (SHARE_0): test:0, test:2 and her set.
    (4, 'test',         pg_temp.ws(5, 1),  NULL::uuid,        pg_temp.minh(),  'edit', pg_temp.lan(),   NULL, interval '5 days'),
    (5, 'test',         pg_temp.ws(5, 3),  NULL::uuid,        pg_temp.trang(), 'use',  pg_temp.lan(),   NULL, interval '10 days'),
    (6, 'test',         pg_temp.ws(5, 3),  NULL::uuid,        pg_temp.bao(),   'use',  pg_temp.lan(),   NULL, interval '10 days'),
    (7, 'question_set', NULL::uuid,        pg_temp.sid(3, 2), pg_temp.minh(),  'use',  pg_temp.lan(),   NULL, interval '2 days')
  ) AS v(n, kind, test_id, set_id, grantee, access, by, message, ago)
ON CONFLICT DO NOTHING;

-- ----------------------------------------------------------------- messages
-- channel and direct open a thread as the first post does, and answer its id: the
-- existing one when the class or the pair already has a thread.
CREATE FUNCTION pg_temp.channel(n int, class uuid, first_at timestamptz) RETURNS uuid
LANGUAGE plpgsql AS $$
BEGIN
  INSERT INTO app.conversations (id, kind, class_id, created_at, last_message_at)
  VALUES (pg_temp.sid(5, n), 'class', class, first_at, first_at)
  ON CONFLICT DO NOTHING;
  RETURN (SELECT id FROM app.conversations WHERE kind = 'class' AND class_id = class);
END;
$$;

CREATE FUNCTION pg_temp.direct(n int, staff uuid, student uuid, first_at timestamptz) RETURNS uuid
LANGUAGE plpgsql AS $$
BEGIN
  INSERT INTO app.conversations (id, kind, staff_id, student_id, created_at, last_message_at)
  VALUES (pg_temp.sid(5, n), 'direct', staff, student, first_at, first_at)
  ON CONFLICT DO NOTHING;
  RETURN (SELECT id FROM app.conversations WHERE kind = 'direct' AND staff_id = staff AND student_id = student);
END;
$$;

-- post writes a message sent at `at`, its id a uuidv7 of that moment so the thread's
-- posts order by id as by time, moves the thread's last_message_at, and answers the
-- message's id. The client id is derived from label, so a second run finds the message
-- and moves nothing.
CREATE FUNCTION pg_temp.post(thread uuid, sender uuid, label text, body text, at timestamptz) RETURNS uuid
LANGUAGE plpgsql AS $$
DECLARE
  inserted int;
BEGIN
  INSERT INTO app.messages (id, conversation_id, sender_id, client_id, body, created_at)
  VALUES (uuidv7(at - clock_timestamp()), thread, sender, pg_temp.did('client:' || label), body, at)
  ON CONFLICT DO NOTHING;
  GET DIAGNOSTICS inserted = ROW_COUNT;
  IF inserted > 0 THEN
    UPDATE app.conversations SET last_message_at = greatest(last_message_at, at) WHERE id = thread;
  END IF;
  RETURN (SELECT id FROM app.messages WHERE sender_id = sender AND client_id = pg_temp.did('client:' || label));
END;
$$;

-- read moves reader's pointer in the thread to message, unless the reader has one.
CREATE FUNCTION pg_temp.read(thread uuid, reader uuid, message uuid, at timestamptz) RETURNS void
LANGUAGE sql AS $$
  INSERT INTO app.conversation_reads (conversation_id, user_id, last_read_message_id, read_at)
  VALUES (thread, reader, message, least(at, now()))
  ON CONFLICT DO NOTHING
$$;

-- read_post gives the first `readers` members of class who have signed in (all of them
-- when NULL), those in first ahead, a pointer at message. Called newest post first, so a
-- member who read the newer post is not moved back by the older.
CREATE FUNCTION pg_temp.read_post(thread uuid, class uuid, message uuid, at timestamptz, readers int, first uuid[])
RETURNS void LANGUAGE sql AS $$
  INSERT INTO app.conversation_reads (conversation_id, user_id, last_read_message_id, read_at)
  SELECT thread, m.user_id, message, least(at, now())
    FROM app.class_members m
    JOIN app.users u ON u.id = m.user_id AND u.disabled_at IS NULL AND u.last_sign_in_at IS NOT NULL
   WHERE m.class_id = class
   ORDER BY m.user_id = ANY (first) DESC, m.user_id
   LIMIT readers
  ON CONFLICT DO NOTHING
$$;

-- Lan's threads. t is the deck's 20:14: Nam's flagged Reading Mock attempt, handed in
-- about four hours before the seed runs (08), so each deck time is t plus its distance
-- from 20:14 and the context card's attempt falls just before his first message.
DO $$
DECLARE
  t timestamptz := coalesce(
    (SELECT coalesce(a.submitted_at, a.started_at) FROM app.attempts a
      WHERE a.assignment_id = pg_temp.ws(7, 1) AND a.student_id = pg_temp.ws(1, 2) AND a.flagged
      ORDER BY a.attempt_no LIMIT 1),
    now() - interval '4 hours 8 minutes');
  nam uuid := pg_temp.ws(1, 2);
  thuha uuid := pg_temp.ws(1, 3);
  giabao uuid := pg_temp.ws(1, 4);
  thao uuid := pg_temp.ws(1, 8);
  thread uuid;
  m1 uuid;
  m2 uuid;
  m3 uuid;
BEGIN
  -- d1 · Lê Hoàng Nam, unread 2.
  thread := pg_temp.direct(4, pg_temp.lan(), nam, t + interval '2 minutes');
  m1 := pg_temp.post(thread, nam, 'd1:1', 'Dạ, lúc làm bài thi thử wifi nhà em bị rớt nên hệ thống tính là em rời khỏi bài ạ.', t + interval '2 minutes');
  m2 := pg_temp.post(thread, nam, 'd1:2', 'Nhờ cô xem lại lượt làm bài giúp em. Em không mở gì khác ạ.', t + interval '6 minutes');
  PERFORM pg_temp.read(thread, nam, m2, t + interval '6 minutes');

  -- d2 · Phạm Thu Hà, unread 1.
  thread := pg_temp.direct(5, pg_temp.lan(), thuha, t - interval '2 hours 16 minutes');
  m1 := pg_temp.post(thread, thuha, 'd2:1', 'Cô ơi, em làm lại bài kiểm tra Unit 3 được không ạ? Em mới được 5/10.', t - interval '2 hours 16 minutes');
  m2 := pg_temp.post(thread, pg_temp.lan(), 'd2:2', 'Được em. Cô mở lại bài cho em đến thứ Sáu. Em xem lại phần nhận xét ở câu 4 trước nhé.', t - interval '2 hours 13 minutes');
  m3 := pg_temp.post(thread, thuha, 'd2:3', 'Em cảm ơn cô nhiều ạ!', t - interval '2 hours 12 minutes');
  PERFORM pg_temp.read(thread, pg_temp.lan(), m2, t - interval '2 hours 13 minutes');
  PERFORM pg_temp.read(thread, thuha, m3, t - interval '2 hours 12 minutes');

  -- d3 · Đỗ Phương Thảo, read.
  thread := pg_temp.direct(6, pg_temp.lan(), thao, t - interval '2 days 10 hours 9 minutes');
  m1 := pg_temp.post(thread, pg_temp.lan(), 'd3:1', 'Điểm bài từ vựng tốt lắm: 24/25!', t - interval '2 days 10 hours 9 minutes');
  m2 := pg_temp.post(thread, thao, 'd3:2', 'Em cảm ơn ạ!', t - interval '2 days 9 hours 52 minutes');
  PERFORM pg_temp.read(thread, pg_temp.lan(), m2, t - interval '2 days 9 hours 40 minutes');
  PERFORM pg_temp.read(thread, thao, m2, t - interval '2 days 9 hours 52 minutes');

  -- The Student deck's t1 · Lan to Nguyễn Gia Bảo, the second message unread.
  thread := pg_temp.direct(7, pg_temp.lan(), giabao, t - interval '2 days 10 hours 12 minutes');
  m1 := pg_temp.post(thread, pg_temp.lan(), 't1:1', 'Chào Bảo, tuần này phần ngữ pháp của em tiến bộ rõ.', t - interval '2 days 10 hours 12 minutes');
  m2 := pg_temp.post(thread, pg_temp.lan(), 't1:2', 'Thứ Năm học Unit 4, em nhớ mang tai nghe. Audio phát hai lần.', t - interval '2 days 10 hours 11 minutes');
  PERFORM pg_temp.read(thread, pg_temp.lan(), m2, t - interval '2 days 10 hours 11 minutes');
  PERFORM pg_temp.read(thread, giabao, m1, t - interval '2 days 10 hours 12 minutes');

  -- c1 · IELTS 6.5 Evening: Monday's post seen by all 22 who have signed in, today's by 21.
  thread := pg_temp.channel(1, pg_temp.ws(3, 1), t - interval '2 days 11 hours 2 minutes');
  m1 := pg_temp.post(thread, pg_temp.lan(), 'c1:1', 'Thứ Sáu này lớp chuyển sang Phòng 3 vì trung tâm diễn tập phòng cháy.', t - interval '2 days 11 hours 2 minutes');
  m2 := pg_temp.post(thread, pg_temp.lan(), 'c1:2', 'Nhắc lại: bài Reading Mock đóng lúc 21:00 tối nay. Nên làm trên laptop.', t - interval '2 hours 44 minutes');
  PERFORM pg_temp.read_post(thread, pg_temp.ws(3, 1), m2, t - interval '1 hour', 21, ARRAY[giabao, nam, thuha]);
  PERFORM pg_temp.read_post(thread, pg_temp.ws(3, 1), m1, t - interval '2 days 8 hours', NULL, ARRAY[]::uuid[]);

  -- c2 · IELTS Foundation A: seen by 13 of 16.
  thread := pg_temp.channel(2, pg_temp.ws(3, 2), t - interval '1 day 3 hours 34 minutes');
  m1 := pg_temp.post(thread, pg_temp.lan(), 'c2:1', 'Bài nghe Unit 4 mở đến 22:00 thứ Năm. Các em nhớ đeo tai nghe, audio phát hai lần.', t - interval '1 day 3 hours 34 minutes');
  PERFORM pg_temp.read_post(thread, pg_temp.ws(3, 2), m1, t - interval '1 day', 13, ARRAY[giabao, thuha]);

  -- c3 · TOEIC 600 Weekend: seen by 20 of 22.
  thread := pg_temp.channel(3, pg_temp.ws(3, 3), t - interval '3 days 12 hours 14 minutes');
  m1 := pg_temp.post(thread, pg_temp.lan(), 'c3:1', 'Chủ nhật này lớp nghỉ. Cả lớp học bù vào chiều thứ Bảy nhé.', t - interval '3 days 12 hours 14 minutes');
  PERFORM pg_temp.read_post(thread, pg_temp.ws(3, 3), m1, t - interval '3 days 6 hours', 20, ARRAY[thao]);
END $$;

-- Khang, created by Minh and put in his class, its only member, as a teacher adds a
-- student: the membership and the tie the classes module writes with it (00167's trigger
-- would add the same tie at the commit). He has signed in, and read what is below.
INSERT INTO app.users
  (id, email, full_name, role_id, password_hash, must_change_password, created_by, created_at,
   last_sign_in_at, last_sign_in_method, last_active_at)
VALUES (pg_temp.khang(), 'khang.dao@quizzivy.com', 'Đào Minh Khang',
        (SELECT id FROM app.roles WHERE builtin_key = 'student'),
        '$argon2id$v=19$m=65536,t=3,p=2$NsEIYu5N8g+iv1W9zV2hfQ$HgTGHdo9uosWEPKpMFDPDSUvBOTCc0oVcPvq7FeVIR4',
        false, pg_temp.minh(), now() - interval '12 days',
        now() - interval '10 days', 'password', now() - interval '22 hours')
ON CONFLICT DO NOTHING;

INSERT INTO app.class_members (class_id, user_id, joined_via, joined_at, added_by)
SELECT pg_temp.minh_class(), pg_temp.khang(), 'admin', now() - interval '10 days', pg_temp.minh()
 WHERE EXISTS (SELECT 1 FROM app.users WHERE id = pg_temp.khang())
ON CONFLICT (class_id, user_id) DO NOTHING;

INSERT INTO app.messaging_ties (staff_id, student_id, since)
SELECT pg_temp.minh(), pg_temp.khang(), now() - interval '10 days'
 WHERE EXISTS (SELECT 1 FROM app.users WHERE id = pg_temp.khang())
ON CONFLICT (staff_id, student_id) DO NOTHING;

-- Minh's announcement and his thread with Khang, all read.
DO $$
DECLARE
  thread uuid;
  m1 uuid;
  m2 uuid;
BEGIN
  IF NOT EXISTS (SELECT 1 FROM app.users WHERE id = pg_temp.khang()) THEN
    RETURN;
  END IF;
  thread := pg_temp.channel(8, pg_temp.minh_class(), now() - interval '2 days');
  m1 := pg_temp.post(thread, pg_temp.minh(), 'minh:c:1', 'Thứ Bảy này lớp mình học ở Phòng 2. Các em nhớ mang theo bút màu nhé.', now() - interval '2 days');
  PERFORM pg_temp.read(thread, pg_temp.khang(), m1, now() - interval '1 day 20 hours');

  thread := pg_temp.direct(9, pg_temp.minh(), pg_temp.khang(), now() - interval '1 day');
  m1 := pg_temp.post(thread, pg_temp.minh(), 'minh:d:1', 'Chào Khang, tuần này em học rất chăm. Thứ Bảy nhớ đến sớm 10 phút nhé.', now() - interval '1 day');
  m2 := pg_temp.post(thread, pg_temp.khang(), 'minh:d:2', 'Dạ, em cảm ơn thầy ạ!', now() - interval '23 hours');
  PERFORM pg_temp.read(thread, pg_temp.khang(), m2, now() - interval '23 hours');
  PERFORM pg_temp.read(thread, pg_temp.minh(), m2, now() - interval '22 hours');
END $$;

COMMIT;
