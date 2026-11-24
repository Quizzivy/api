-- Development seed, part fourteen: colleague threads and a student in no class (F2, T-F2.9).
--
-- Relies on 06 (giaovien@quizzivy.com, Trần Thị Lan, and giaovien2@quizzivy.com, Lê Văn
-- Minh). It adds no teacher: Minh, the live suite's TEACHER_2, is the second one. Roles and
-- the colleague columns come from migrations (00180).
--
-- The colleague thread between Lan and Minh, shaped like the F2 Messaging proposal's
-- Colleagues thread (COLL's k1: SAY.du1 with FILE_CUE, then a reply and a thank-you), with
-- Minh in Phan Minh Đức's place and Lan in Giàu's:
--   three days ago   Minh   Chị Lan ơi, em gửi chị bộ thẻ gợi ý …   + Thẻ gợi ý Speaking Part 2.pdf
--   today            Lan    Cảm ơn em, bộ thẻ dễ dùng lắm. …
--   today            Minh   Dạ, chị dùng thấy cần sửa gì thì nhắn em nhé.
-- Nothing is unread on either side, so a live spec's first post in it makes the other's
-- unread count exactly one. The pair is stored low-first: Lan (…0601) is colleague_low_id,
-- Minh (…0602) colleague_high_id.
--
-- The PDF: a one-page file (672 bytes) whose bytes are below, so the row's size and
-- checksum are the file's. The seed has no object-store access (README.md, "Media
-- objects"), so the object at messages/01935000-0000-7000-8000-000001400301.pdf is absent
-- and the thread lists the file but a download of it answers NoSuchKey until the bytes are
-- copied there (README.md, "14: the colleague PDF"). A live spec that downloads a file
-- uploads its own.
--
-- Hồ Ngọc Diệp (diep.ho@quizzivy.com), a student in no class, enabled, signed in once,
-- created by no one and tied to no one, with no thread: the student only F2's rule (any
-- teacher or admin may write to any enabled student) lets staff write to. 07's thirty are
-- in no class too until the join spec joins them, so she is the one a spec can count on.
--
-- The live spec's third person is 08's Bùi Ngọc Mai (ngocmai.bui@quizzivy.com), in Lan's
-- IELTS 6.5 Evening and never taught by the Admin, so the Admin's first post to her reads
-- "Teacher at the centre" on her side; 99-assert.sql holds her to that, and nothing here
-- writes her.
--
-- Written as the API writes it: the thread is opened by its first post, a message's id is a
-- uuidv7 of the moment it was sent, last_message_at is the newest post, a file is uploaded
-- by its sender a moment before the post that names it, and messages are only inserted.
--
-- Ids are fixed: pg_temp.sid(kind, n) is …00000140 + kind + n. Kinds: 1 users,
-- 2 conversations, 3 attachments. A message is found again by its sender and client id,
-- which did makes. Idempotent like the other seeds: a row that exists is left as it is.
-- One transaction.

BEGIN;

CREATE FUNCTION pg_temp.sid(kind int, n int) RETURNS uuid
LANGUAGE sql IMMUTABLE AS $$
  SELECT ('01935000-0000-7000-8000-00000140' || lpad(to_hex(kind), 2, '0') || lpad(to_hex(n), 2, '0'))::uuid
$$;

CREATE FUNCTION pg_temp.did(label text) RETURNS uuid
LANGUAGE sql IMMUTABLE AS $$
  SELECT ('01935000-0000-7000-8000-' || substr(md5('14-dev-colleagues:' || label), 1, 12))::uuid
$$;

CREATE FUNCTION pg_temp.lan() RETURNS uuid LANGUAGE sql IMMUTABLE AS $$ SELECT '01935000-0000-7000-8000-000000000601'::uuid $$;
CREATE FUNCTION pg_temp.minh() RETURNS uuid LANGUAGE sql IMMUTABLE AS $$ SELECT '01935000-0000-7000-8000-000000000602'::uuid $$;

-- The PDF's bytes, one page: "Speaking Part 2 cue cards".
CREATE FUNCTION pg_temp.cue_cards() RETURNS bytea LANGUAGE sql IMMUTABLE AS $$ SELECT decode('JVBERi0xLjQKMSAwIG9iago8PCAvVHlwZSAvQ2F0YWxvZyAvUGFnZXMgMiAwIFIgPj4KZW5kb2JqCjIgMCBvYmoKPDwgL1R5cGUgL1BhZ2VzIC9LaWRzIFszIDAgUl0gL0NvdW50IDEgPj4KZW5kb2JqCjMgMCBvYmoKPDwgL1R5cGUgL1BhZ2UgL1BhcmVudCAyIDAgUiAvTWVkaWFCb3ggWzAgMCA1OTUgODQyXSAvUmVzb3VyY2VzIDw8IC9Gb250IDw8IC9GMSA0IDAgUiA+PiA+PiAvQ29udGVudHMgNSAwIFIgPj4KZW5kb2JqCjQgMCBvYmoKPDwgL1R5cGUgL0ZvbnQgL1N1YnR5cGUgL1R5cGUxIC9CYXNlRm9udCAvSGVsdmV0aWNhID4+CmVuZG9iago1IDAgb2JqCjw8IC9MZW5ndGggMTI3ID4+CnN0cmVhbQpCVCAvRjEgMTggVGYgNzIgNzYwIFRkIChTcGVha2luZyBQYXJ0IDIgY3VlIGNhcmRzKSBUaiAwIC0zMiBUZCAvRjEgMTIgVGYgKERlc2NyaWJlIGEgcGxhY2UgeW91IHZpc2l0ZWQgd2l0aCB5b3VyIGZhbWlseS4pIFRqIEVUCmVuZHN0cmVhbQplbmRvYmoKeHJlZgowIDYKMDAwMDAwMDAwMCA2NTUzNSBmIAowMDAwMDAwMDA5IDAwMDAwIG4gCjAwMDAwMDAwNTggMDAwMDAgbiAKMDAwMDAwMDExNSAwMDAwMCBuIAowMDAwMDAwMjQxIDAwMDAwIG4gCjAwMDAwMDAzMTEgMDAwMDAgbiAKdHJhaWxlcgo8PCAvU2l6ZSA2IC9Sb290IDEgMCBSID4+CnN0YXJ0eHJlZgo0ODkKJSVFT0YK', 'base64') $$;

-- ----------------------------------------------------------- the student
-- Signed in once, so the Admin console reads her as active; no class, no tie, no creator.
INSERT INTO app.users
  (id, email, full_name, role_id, password_hash, must_change_password, created_at,
   last_sign_in_at, last_sign_in_method, last_active_at)
VALUES (pg_temp.sid(1, 1), 'diep.ho@quizzivy.com', 'Hồ Ngọc Diệp',
        (SELECT id FROM app.roles WHERE builtin_key = 'student'),
        '$argon2id$v=19$m=65536,t=3,p=2$NsEIYu5N8g+iv1W9zV2hfQ$HgTGHdo9uosWEPKpMFDPDSUvBOTCc0oVcPvq7FeVIR4',
        false, now() - interval '6 days',
        now() - interval '5 days', 'password', now() - interval '5 days')
ON CONFLICT DO NOTHING;

-- ------------------------------------------------------- the colleague thread
-- colleague opens the pair's thread as the first post does, low id first, and answers its
-- id: the existing one when the pair already has a thread.
CREATE FUNCTION pg_temp.colleague(n int, a uuid, b uuid, first_at timestamptz) RETURNS uuid
LANGUAGE plpgsql AS $$
BEGIN
  INSERT INTO app.conversations (id, kind, colleague_low_id, colleague_high_id, created_at, last_message_at)
  VALUES (pg_temp.sid(2, n), 'colleague', least(a, b), greatest(a, b), first_at, first_at)
  ON CONFLICT DO NOTHING;
  RETURN (SELECT id FROM app.conversations
           WHERE kind = 'colleague' AND colleague_low_id = least(a, b) AND colleague_high_id = greatest(a, b));
END;
$$;

-- attach records the file sender uploaded at `at`, as the upload does (messages/{id}.pdf,
-- the size and checksum of its bytes), unless its id is taken, and answers its id.
CREATE FUNCTION pg_temp.attach(n int, sender uuid, filename text, bytes bytea, at timestamptz) RETURNS uuid
LANGUAGE sql AS $$
  INSERT INTO app.message_attachments (id, uploaded_by, storage_key, mime_type, bytes, original_filename, checksum_sha256, created_at)
  VALUES (pg_temp.sid(3, n), sender, 'messages/' || pg_temp.sid(3, n) || '.pdf', 'application/pdf',
          octet_length(bytes), filename, sha256(bytes), at)
  ON CONFLICT DO NOTHING;
  SELECT pg_temp.sid(3, n);
$$;

-- post writes a message sent at `at` (11's post, with a file), its id a uuidv7 of that
-- moment, moves the thread's last_message_at, and answers the message's id. A second run
-- finds the message by its client id and moves nothing.
CREATE FUNCTION pg_temp.post(thread uuid, sender uuid, label text, body text, file uuid, at timestamptz) RETURNS uuid
LANGUAGE plpgsql AS $$
DECLARE
  inserted int;
BEGIN
  INSERT INTO app.messages (id, conversation_id, sender_id, client_id, body, attachment_id, created_at)
  VALUES (uuidv7(at - clock_timestamp()), thread, sender, pg_temp.did('client:' || label), body, file, at)
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

DO $$
DECLARE
  first_at timestamptz := now() - interval '2 days 22 hours';
  thread uuid;
  file uuid;
  m1 uuid;
  m2 uuid;
  m3 uuid;
BEGIN
  thread := pg_temp.colleague(1, pg_temp.minh(), pg_temp.lan(), first_at);
  file := pg_temp.attach(1, pg_temp.minh(), 'Thẻ gợi ý Speaking Part 2.pdf', pg_temp.cue_cards(), first_at - interval '40 seconds');
  m1 := pg_temp.post(thread, pg_temp.minh(), 'k1:1', 'Chị Lan ơi, em gửi chị bộ thẻ gợi ý em dùng cho lớp tối thứ Bảy.', file, first_at);
  m2 := pg_temp.post(thread, pg_temp.lan(), 'k1:2', 'Cảm ơn em, bộ thẻ dễ dùng lắm. Tuần này chị thử với lớp IELTS 6.5 Evening.', NULL, now() - interval '4 hours 12 minutes');
  m3 := pg_temp.post(thread, pg_temp.minh(), 'k1:3', 'Dạ, chị dùng thấy cần sửa gì thì nhắn em nhé.', NULL, now() - interval '3 hours 50 minutes');
  PERFORM pg_temp.read(thread, pg_temp.lan(), m3, now() - interval '3 hours 41 minutes');
  PERFORM pg_temp.read(thread, pg_temp.minh(), m3, now() - interval '3 hours 50 minutes');
END $$;

COMMIT;
