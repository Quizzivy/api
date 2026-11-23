-- Seed self-check. Runs last.
--
-- The seed writes frozen version rows directly, which is the one path that
-- skips publish's validation (internal/tests/publish/validate.go) -- and so
-- the one path that can produce data the product cannot. A choice question
-- with no options renders as a prompt and nothing else, and every answer a
-- client builds for it is rejected as invalid. Refuse the seed instead.
DO $$
DECLARE
  bad text;
BEGIN
  -- Bank: choice questions carry at least two options and a correct one.
  SELECT string_agg(q.id::text, ', ') INTO bad
    FROM app.questions q
   WHERE q.type IN ('single_choice', 'multiple_choice')
     AND q.deleted_at IS NULL
     AND (SELECT count(*) FROM app.question_options o WHERE o.question_id = q.id) < 2;
  IF bad IS NOT NULL THEN
    RAISE EXCEPTION 'seed: bank choice question(s) with fewer than two options: %', bad;
  END IF;

  -- Ties: every member is tied to their class's teacher, as the binary writes it with the
  -- enrolment (shared/ties). Since 00179 no trigger writes it, so a seed that adds a member
  -- writes the tie too, or the teacher cannot write to the student first.
  SELECT string_agg(m.class_id::text || '/' || m.user_id::text, ', ') INTO bad
    FROM app.class_members m
    JOIN app.classes c ON c.id = m.class_id
   WHERE c.teacher_id <> m.user_id
     AND NOT EXISTS (SELECT 1 FROM app.messaging_ties t WHERE t.staff_id = c.teacher_id AND t.student_id = m.user_id);
  IF bad IS NOT NULL THEN
    RAISE EXCEPTION 'seed: member(s) not tied to their class''s teacher: %', bad;
  END IF;

  SELECT string_agg(q.id::text, ', ') INTO bad
    FROM app.questions q
   WHERE q.type IN ('single_choice', 'multiple_choice')
     AND q.deleted_at IS NULL
     AND NOT EXISTS (SELECT 1 FROM app.question_options o
                      WHERE o.question_id = q.id AND o.is_correct);
  IF bad IS NOT NULL THEN
    RAISE EXCEPTION 'seed: bank choice question(s) with no correct option: %', bad;
  END IF;

  -- Frozen: the same rules, on the rows a student actually answers.
  SELECT string_agg(q.id::text, ', ') INTO bad
    FROM app.test_version_questions q
   WHERE q.type IN ('single_choice', 'multiple_choice')
     AND (SELECT count(*) FROM app.test_version_options o
           WHERE o.test_version_question_id = q.id) < 2;
  IF bad IS NOT NULL THEN
    RAISE EXCEPTION 'seed: frozen choice question(s) with fewer than two options: %', bad;
  END IF;

  SELECT string_agg(q.id::text, ', ') INTO bad
    FROM app.test_version_questions q
   WHERE q.type IN ('single_choice', 'multiple_choice')
     AND NOT EXISTS (SELECT 1 FROM app.test_version_options o
                      WHERE o.test_version_question_id = q.id AND o.is_correct);
  IF bad IS NOT NULL THEN
    RAISE EXCEPTION 'seed: frozen choice question(s) with no correct option: %', bad;
  END IF;

  SELECT string_agg(q.id::text, ', ') INTO bad
    FROM app.test_version_questions q
   WHERE q.type = 'single_choice'
     AND (SELECT count(*) FROM app.test_version_options o
           WHERE o.test_version_question_id = q.id AND o.is_correct) > 1;
  IF bad IS NOT NULL THEN
    RAISE EXCEPTION 'seed: frozen single_choice question(s) with more than one correct option: %', bad;
  END IF;

  SELECT string_agg(q.id::text, ', ') INTO bad
    FROM app.test_version_questions q
   WHERE q.type = 'fill_blank'
     AND NOT EXISTS (SELECT 1 FROM app.test_version_blanks b
                      WHERE b.test_version_question_id = q.id);
  IF bad IS NOT NULL THEN
    RAISE EXCEPTION 'seed: frozen fill_blank question(s) with no blanks: %', bad;
  END IF;

  -- A seeded answer must point at options that exist on its own question.
  -- Seeded rows only: every seed id starts with 01935000-, and what students
  -- and tests wrote into a dev database since is not the seed's to vouch for.
  SELECT string_agg(a.attempt_id::text || '/' || a.question_id::text, ', ') INTO bad
    FROM app.attempt_answers a
   WHERE a.attempt_id::text LIKE '01935000-%'
     AND jsonb_typeof(a.payload->'optionIds') = 'array'
     AND EXISTS (
       SELECT 1 FROM jsonb_array_elements_text(a.payload->'optionIds') chosen(id)
        WHERE NOT EXISTS (SELECT 1 FROM app.test_version_options o
                           WHERE o.id::text = chosen.id
                             AND o.test_version_question_id = a.question_id));
  IF bad IS NOT NULL THEN
    RAISE EXCEPTION 'seed: attempt answer(s) choosing options that do not exist: %', bad;
  END IF;

  -- R2's access model: the built-in roles exist, and every row it scopes by
  -- has the column it reads (T-R2.11).
  IF (SELECT count(*) FROM app.roles
       WHERE builtin_key IN ('admin', 'teacher', 'assistant', 'student')) <> 4 THEN
    RAISE EXCEPTION 'seed: the four built-in roles are not all present';
  END IF;

  SELECT string_agg(email, ', ') INTO bad FROM app.users WHERE role_id IS NULL;
  IF bad IS NOT NULL THEN
    RAISE EXCEPTION 'seed: user(s) without role_id: %', bad;
  END IF;

  SELECT string_agg(id::text, ', ') INTO bad FROM app.classes WHERE teacher_id IS NULL;
  IF bad IS NOT NULL THEN
    RAISE EXCEPTION 'seed: class(es) without teacher_id: %', bad;
  END IF;

  SELECT string_agg(owned.kind || ' ' || owned.id::text, ', ') INTO bad
    FROM (SELECT 'test' AS kind, id FROM app.tests WHERE owner_id IS NULL
          UNION ALL SELECT 'question', id FROM app.questions WHERE owner_id IS NULL
          UNION ALL SELECT 'question group', id FROM app.question_groups WHERE owner_id IS NULL
          UNION ALL SELECT 'media asset', id FROM app.media_assets WHERE owner_id IS NULL) owned;
  IF bad IS NOT NULL THEN
    RAISE EXCEPTION 'seed: row(s) without owner_id: %', bad;
  END IF;

  RAISE NOTICE 'seed: invariants hold';
END $$;

-- 08's teacher workspace (T-R4.51): the rows exist in the shapes the Teacher
-- deck's fixtures need. Every id here starts 01935000-0000-7000-8000-00000080.
DO $$
DECLARE
  lan constant uuid := '01935000-0000-7000-8000-000000000601';
  mine constant text := '01935000-0000-7000-8000-00000080%';
  showcase uuid;
  bad text;
  found int;
BEGIN
  SELECT count(*) INTO found FROM app.tests WHERE id::text LIKE mine AND owner_id = lan AND created_by = lan;
  IF found <> 7 THEN
    RAISE EXCEPTION 'seed: expected seven tests owned by the teacher, found %', found;
  END IF;

  -- One test with three versions and a draft ahead of the third: a bank
  -- question in the draft that the third version does not hold, and a prompt
  -- the bank has since corrected.
  SELECT count(*) INTO found
    FROM app.tests t
   WHERE t.id::text LIKE mine AND t.status = 'published' AND t.current_version = 3 AND t.last_published_version = 3
     AND (SELECT count(*) FROM app.test_versions v WHERE v.test_id = t.id) = 3
     AND EXISTS (SELECT 1 FROM app.test_sections s JOIN app.test_section_questions m ON m.test_section_id = s.id
                  WHERE s.test_id = t.id
                    AND NOT EXISTS (SELECT 1 FROM app.test_version_questions f
                                      JOIN app.test_version_sections vs ON vs.id = f.test_version_section_id
                                      JOIN app.test_versions v ON v.id = vs.test_version_id AND v.test_id = t.id AND v.version = 3
                                     WHERE f.source_question_id = m.question_id))
     AND EXISTS (SELECT 1 FROM app.test_version_questions f
                   JOIN app.questions q ON q.id = f.source_question_id
                   JOIN app.test_version_sections vs ON vs.id = f.test_version_section_id
                   JOIN app.test_versions v ON v.id = vs.test_version_id AND v.test_id = t.id AND v.version = 3
                  WHERE f.prompt <> q.prompt);
  IF found <> 1 THEN
    RAISE EXCEPTION 'seed: expected one published test with three versions and a draft ahead, found %', found;
  END IF;

  -- A version is written as publish writes it.
  SELECT string_agg(v.id::text, ', ') INTO bad
    FROM app.test_versions v
   WHERE v.test_id::text LIKE mine
     AND (v.delivery_version <> 'group_v1'
          OR v.total_points IS DISTINCT FROM (SELECT sum(f.points)
                                                FROM app.test_version_questions f
                                                JOIN app.test_version_sections vs ON vs.id = f.test_version_section_id
                                               WHERE vs.test_version_id = v.id));
  IF bad IS NOT NULL THEN
    RAISE EXCEPTION 'seed: version(s) not written as publish writes them: %', bad;
  END IF;

  SELECT count(*) INTO found FROM app.media_assets
   WHERE id::text LIKE mine AND owner_id = lan AND uploaded_by = lan AND deleted_at IS NULL;
  IF found <> 8 THEN
    RAISE EXCEPTION 'seed: expected eight media files owned by the teacher, found %', found;
  END IF;

  SELECT string_agg(c.name || '=' || c.members, ', ') INTO bad
    FROM (SELECT c.name, (SELECT count(*) FROM app.class_members m WHERE m.class_id = c.id) AS members
            FROM app.classes c WHERE c.id::text LIKE mine AND c.teacher_id = lan) c
   WHERE (c.name, c.members) NOT IN (('IELTS 6.5 Evening', 24), ('IELTS Foundation A', 16), ('TOEIC 600 Weekend', 22), ('Kids Starters B', 12));
  SELECT count(*) INTO found FROM app.classes WHERE id::text LIKE mine AND teacher_id = lan;
  IF bad IS NOT NULL OR found <> 4 THEN
    RAISE EXCEPTION 'seed: expected four classes with the deck''s members, found % (off: %)', found, bad;
  END IF;

  SELECT count(DISTINCT CASE
           WHEN a.published_at IS NULL THEN 'draft'
           WHEN a.closed_at IS NOT NULL AND now() >= a.closed_at THEN 'closed'
           WHEN now() < a.opens_at THEN 'scheduled'
           WHEN now() < a.closes_at THEN 'open'
           ELSE 'closed' END) INTO found
    FROM app.assignments a WHERE a.id::text LIKE mine AND a.created_by = lan;
  IF found <> 4 THEN
    RAISE EXCEPTION 'seed: expected assignments in all four statuses, found % statuses', found;
  END IF;

  SELECT count(*) INTO found FROM app.attempts a
   WHERE a.assignment_id::text LIKE mine AND a.flagged AND a.focus_loss_count > 0
     AND EXISTS (SELECT 1 FROM app.attempt_events e WHERE e.attempt_id = a.id AND e.kind = 'fullscreen_exit');
  IF found < 1 THEN
    RAISE EXCEPTION 'seed: no flagged attempt with its integrity events';
  END IF;

  SELECT count(DISTINCT a.id) INTO found FROM app.attempts a
    JOIN app.attempt_answers aa ON aa.attempt_id = a.id
   WHERE a.assignment_id::text LIKE mine AND a.status IN ('submitted', 'timed_out')
     AND aa.requires_manual AND aa.manual_score IS NULL;
  IF found <> 14 THEN
    RAISE EXCEPTION 'seed: expected fourteen attempts waiting to be graded, found %', found;
  END IF;

  SELECT string_agg(a.id::text, ', ') INTO bad FROM app.attempts a
   WHERE a.assignment_id::text LIKE mine AND a.status IN ('submitted', 'graded', 'timed_out')
     AND a.score_earned IS DISTINCT FROM (SELECT coalesce(sum(aa.final_score), 0) FROM app.attempt_answers aa WHERE aa.attempt_id = a.id);
  IF bad IS NOT NULL THEN
    RAISE EXCEPTION 'seed: attempt(s) whose score is not the sum of their answers: %', bad;
  END IF;

  -- Four imports in four states; the pasted one is in review with two open
  -- actions and one thing to confirm.
  SELECT string_agg(i.status, ', ' ORDER BY i.status) INTO bad
    FROM app.word_imports i WHERE i.id::text LIKE mine AND i.created_by = lan;
  IF bad IS DISTINCT FROM 'committed, failed, needs_review, processing' THEN
    RAISE EXCEPTION 'seed: expected four imports in four states, found %', bad;
  END IF;
  SELECT count(*) INTO found
    FROM app.word_imports i
    JOIN app.word_import_sources s ON s.import_id = i.id AND s.format = 'text' AND s.characters > 0
    JOIN app.word_import_drafts d ON d.import_id = i.id
   WHERE i.id::text LIKE mine AND i.status = 'needs_review' AND d.open_action_count = 2 AND d.open_confirm_count = 1;
  IF found <> 1 THEN
    RAISE EXCEPTION 'seed: no pasted-text import in review with two open actions and one to confirm';
  END IF;
  SELECT count(*) INTO found
    FROM app.word_import_commits c JOIN app.tests t ON t.id = c.test_id
   WHERE c.import_id::text LIKE mine AND t.owner_id = lan AND t.status = 'draft';
  IF found <> 1 THEN
    RAISE EXCEPTION 'seed: the committed import does not point at a draft test';
  END IF;

  -- One test holds a question of each content kind, in the bank and in its
  -- published version.
  SELECT t.id INTO showcase FROM app.tests t WHERE t.id::text LIKE mine AND t.title LIKE 'Unit 4%';
  SELECT string_agg(kind.name, ', ') INTO bad
    FROM (VALUES
      ('table and link'), ('two gaps'), ('markdown'), ('audio'), ('image with alt')
    ) AS kind(name)
   WHERE NOT EXISTS (
     SELECT 1
       FROM app.test_sections s
       JOIN app.test_section_questions m ON m.test_section_id = s.id
       JOIN app.questions q ON q.id = m.question_id
      WHERE s.test_id = showcase
        AND CASE kind.name
              WHEN 'table and link' THEN jsonb_path_exists(q.prompt_content, '$.blocks[*] ? (@.type == "table")')
                                     AND jsonb_path_exists(q.prompt_content, '$.** ? (@.type == "link")')
              WHEN 'two gaps' THEN q.type = 'fill_blank' AND q.prompt_content IS NOT NULL
                               AND (SELECT count(*) FROM app.question_blanks b WHERE b.question_id = q.id AND b.gap_id IS NOT NULL) = 2
              WHEN 'markdown' THEN q.prompt_content IS NULL AND q.prompt LIKE '%**%'
              WHEN 'audio' THEN q.media_asset_kind = 'audio' AND q.transcript IS NOT NULL
              ELSE q.media_asset_kind = 'image' AND q.media_alt IS NOT NULL END);
  IF bad IS NOT NULL THEN
    RAISE EXCEPTION 'seed: the content-kind test lacks: %', bad;
  END IF;
  SELECT count(*) INTO found
    FROM app.test_version_questions f
    JOIN app.test_version_sections vs ON vs.id = f.test_version_section_id
    JOIN app.test_versions v ON v.id = vs.test_version_id
   WHERE v.test_id = showcase AND (f.prompt_content IS NOT NULL OR f.media_alt IS NOT NULL OR f.media_asset_kind = 'audio');
  IF found < 4 THEN
    RAISE EXCEPTION 'seed: the content-kind test''s version froze fewer rich questions than the bank holds: %', found;
  END IF;

  RAISE NOTICE 'seed: teacher workspace holds';
END $$;

-- The Admin console (09): the deck's shape, so every screen and state of
-- /admin has rows to draw.
DO $$
DECLARE
  found integer;
  bad text;
BEGIN
  SELECT count(*) INTO found FROM app.users u JOIN app.roles r ON r.id = u.role_id WHERE r.builtin_key = 'admin';
  IF found < 3 THEN
    RAISE EXCEPTION 'seed: expected at least three Admins, found %', found;
  END IF;
  SELECT count(*) INTO found FROM app.users u JOIN app.roles r ON r.id = u.role_id WHERE r.builtin_key = 'teacher';
  IF found < 9 THEN
    RAISE EXCEPTION 'seed: expected at least nine Teachers, found %', found;
  END IF;
  SELECT count(*) INTO found
    FROM app.users u JOIN app.roles r ON r.id = u.role_id AND r.builtin_key = 'teacher'
   WHERE u.disabled_at IS NOT NULL AND EXISTS (SELECT 1 FROM app.classes c WHERE c.teacher_id = u.id);
  IF found < 1 THEN
    RAISE EXCEPTION 'seed: expected a disabled Teacher who teaches a class, found %', found;
  END IF;

  SELECT string_agg(s.status, ', ') INTO bad
    FROM (VALUES ('active'), ('must_change_password'), ('never_signed_in'), ('disabled')) AS s(status)
   WHERE NOT EXISTS (
     SELECT 1 FROM app.users u JOIN app.roles r ON r.id = u.role_id AND r.builtin_key = 'student'
      WHERE CASE s.status
              WHEN 'disabled' THEN u.disabled_at IS NOT NULL
              WHEN 'must_change_password' THEN u.disabled_at IS NULL AND u.must_change_password
              WHEN 'never_signed_in' THEN u.disabled_at IS NULL AND NOT u.must_change_password AND u.last_sign_in_at IS NULL
              ELSE u.disabled_at IS NULL AND NOT u.must_change_password AND u.last_sign_in_at IS NOT NULL END);
  IF bad IS NOT NULL THEN
    RAISE EXCEPTION 'seed: no student in status %', bad;
  END IF;

  SELECT count(*) INTO found FROM app.classes WHERE archived_at IS NOT NULL;
  IF found <> 1 THEN
    RAISE EXCEPTION 'seed: expected one archived class, found %', found;
  END IF;
  SELECT count(*) INTO found FROM app.roles WHERE builtin_key IS NULL AND name = 'Tổ trưởng';
  IF found <> 1 THEN
    RAISE EXCEPTION 'seed: the custom role Tổ trưởng is missing';
  END IF;

  SELECT string_agg(c.category, ', ') INTO bad
    FROM (VALUES ('user'), ('class'), ('attempt'), ('test'), ('media_asset'), ('word_import')) AS c(category)
   WHERE NOT EXISTS (SELECT 1 FROM app.audit_log a WHERE a.entity = c.category);
  IF bad IS NOT NULL THEN
    RAISE EXCEPTION 'seed: no audit entry about %', bad;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM app.audit_log WHERE actor_user_id IS NULL) THEN
    RAISE EXCEPTION 'seed: no audit entry written by the system';
  END IF;

  SELECT count(DISTINCT day) INTO found FROM app.user_activity_days WHERE day > current_date - 14;
  IF found <> 14 THEN
    RAISE EXCEPTION 'seed: expected fourteen days of activity, found %', found;
  END IF;

  RAISE NOTICE 'seed: admin console holds';
END $$;

-- R6's types (T-R6.14): the publish rules T-R6.4 holds a frozen question to,
-- on every frozen row, then 10's paper holds one question of each type.
DO $$
DECLARE
  paper constant uuid := '01935000-0000-7000-8000-000000001002';
  bad text;
BEGIN
  -- tfng: the three fixed options in their fixed order, exactly one correct.
  SELECT string_agg(q.id::text, ', ') INTO bad
    FROM app.test_version_questions q
   WHERE q.type = 'tfng'
     AND ((SELECT array_agg(o.text ORDER BY o.ordinal) FROM app.test_version_options o
            WHERE o.test_version_question_id = q.id) IS DISTINCT FROM ARRAY['True', 'False', 'Not given']
          OR (SELECT count(*) FROM app.test_version_options o
               WHERE o.test_version_question_id = q.id AND o.is_correct) <> 1);
  IF bad IS NOT NULL THEN
    RAISE EXCEPTION 'seed: frozen tfng question(s) without True, False, Not given and one key: %', bad;
  END IF;

  -- true / false: two options, one correct.
  SELECT string_agg(q.id::text, ', ') INTO bad
    FROM app.test_version_questions q
   WHERE q.type = 'true_false'
     AND ((SELECT count(*) FROM app.test_version_options o WHERE o.test_version_question_id = q.id) <> 2
          OR (SELECT count(*) FROM app.test_version_options o
               WHERE o.test_version_question_id = q.id AND o.is_correct) <> 1);
  IF bad IS NOT NULL THEN
    RAISE EXCEPTION 'seed: frozen true_false question(s) without two options and one key: %', bad;
  END IF;

  -- "Choose N": N correct options, and at least one more option than N.
  SELECT string_agg(q.id::text, ', ') INTO bad
    FROM app.test_version_questions q
   WHERE q.select_count IS NOT NULL
     AND ((SELECT count(*) FROM app.test_version_options o
            WHERE o.test_version_question_id = q.id AND o.is_correct) <> q.select_count
          OR (SELECT count(*) FROM app.test_version_options o
               WHERE o.test_version_question_id = q.id) <= q.select_count);
  IF bad IS NOT NULL THEN
    RAISE EXCEPTION 'seed: frozen "choose N" question(s) whose keys do not number N: %', bad;
  END IF;

  -- Matching: 2 to 12 choices, none marked correct; 2 to 20 items; without
  -- reuse, no more items than choices and no choice keyed twice.
  SELECT string_agg(q.id::text, ', ') INTO bad
    FROM app.test_version_questions q
    CROSS JOIN LATERAL (
      SELECT (SELECT count(*) FROM app.test_version_options o WHERE o.test_version_question_id = q.id) AS choices,
             (SELECT count(*) FROM app.test_version_options o
               WHERE o.test_version_question_id = q.id AND o.is_correct) AS keyed,
             (SELECT count(*) FROM app.test_version_match_items i WHERE i.test_version_question_id = q.id) AS items,
             (SELECT count(DISTINCT i.correct_option_id) FROM app.test_version_match_items i
               WHERE i.test_version_question_id = q.id) AS answers) n
   WHERE q.type = 'matching'
     AND (n.choices NOT BETWEEN 2 AND 12 OR n.keyed <> 0 OR n.items NOT BETWEEN 2 AND 20
          OR (NOT q.match_allow_reuse AND (n.items > n.choices OR n.answers <> n.items)));
  IF bad IS NOT NULL THEN
    RAISE EXCEPTION 'seed: frozen matching question(s) that break the choice or item rules: %', bad;
  END IF;

  SELECT string_agg(DISTINCT i.test_version_question_id::text, ', ') INTO bad
    FROM app.test_version_match_items i
    JOIN app.test_version_questions q ON q.id = i.test_version_question_id
   WHERE q.type <> 'matching';
  IF bad IS NOT NULL THEN
    RAISE EXCEPTION 'seed: match items on frozen question(s) that are not matching: %', bad;
  END IF;

  -- Essays: at most six criteria, labels distinct; nothing else has a rubric.
  SELECT string_agg(q.id::text, ', ') INTO bad
    FROM app.test_version_questions q
   WHERE EXISTS (SELECT 1 FROM app.test_version_rubric_criteria c WHERE c.test_version_question_id = q.id)
     AND (q.type <> 'essay'
          OR (SELECT count(*) FROM app.test_version_rubric_criteria c WHERE c.test_version_question_id = q.id) > 6
          OR (SELECT count(DISTINCT lower(btrim(c.label))) FROM app.test_version_rubric_criteria c
               WHERE c.test_version_question_id = q.id)
             <> (SELECT count(*) FROM app.test_version_rubric_criteria c WHERE c.test_version_question_id = q.id));
  IF bad IS NOT NULL THEN
    RAISE EXCEPTION 'seed: frozen rubric(s) that break the essay rules: %', bad;
  END IF;

  -- Media in a prompt or an explanation: every asset the content names has
  -- its reference row, and no row names an asset the content does not.
  SELECT string_agg(DISTINCT coalesce(named.qid, ref.qid)::text, ', ') INTO bad
    FROM (SELECT q.id AS qid, f.field, (b.block->>'assetId')::uuid AS asset
            FROM app.test_version_questions q
            CROSS JOIN LATERAL (VALUES ('prompt', q.prompt_content), ('explanation', q.explanation_content)) f(field, doc)
            CROSS JOIN LATERAL jsonb_array_elements(coalesce(f.doc->'blocks', '[]'::jsonb)) b(block)
           WHERE b.block->>'type' IN ('image', 'audio') AND b.block ? 'assetId') named
    FULL JOIN (SELECT r.test_version_question_id AS qid, r.field, r.media_asset_id AS asset
                 FROM app.test_version_question_media_references r) ref
      ON ref.qid = named.qid AND ref.field = named.field AND ref.asset = named.asset
   WHERE named.qid IS NULL OR ref.qid IS NULL;
  IF bad IS NOT NULL THEN
    RAISE EXCEPTION 'seed: frozen question(s) whose media references differ from their content: %', bad;
  END IF;

  -- 10's paper: one question of every type, a group, four criteria and media
  -- in a prompt and an explanation.
  SELECT string_agg(t.type::text, ', ') INTO bad
    FROM unnest(enum_range(NULL::app.question_type)) AS t(type)
   WHERE NOT EXISTS (
     SELECT 1 FROM app.test_version_questions q
       JOIN app.test_version_sections s ON s.id = q.test_version_section_id
      WHERE s.test_version_id = paper AND q.type = t.type);
  IF bad IS NOT NULL THEN
    RAISE EXCEPTION 'seed: the every-type paper has no question of type %', bad;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM app.test_version_groups g
                   JOIN app.test_version_sections s ON s.id = g.test_version_section_id
                  WHERE s.test_version_id = paper)
     OR (SELECT count(*) FROM app.test_version_rubric_criteria c
           JOIN app.test_version_questions q ON q.id = c.test_version_question_id
           JOIN app.test_version_sections s ON s.id = q.test_version_section_id
          WHERE s.test_version_id = paper) <> 4
     OR (SELECT count(DISTINCT r.field) FROM app.test_version_question_media_references r
           JOIN app.test_version_questions q ON q.id = r.test_version_question_id
           JOIN app.test_version_sections s ON s.id = q.test_version_section_id
          WHERE s.test_version_id = paper) <> 2 THEN
    RAISE EXCEPTION 'seed: the every-type paper lacks its reading group, four criteria or prompt and explanation media';
  END IF;

  RAISE NOTICE 'seed: question types hold';
END $$;

-- 11's collaboration (T-R7.24): grants, question sets and messages as the API writes
-- them, then the decks' shapes. Every id 11 fixes starts 01935000-0000-7000-8000-00000110,
-- and its messages are those of its threads.
DO $$
DECLARE
  mine constant text := '01935000-0000-7000-8000-00000110%';
  lan constant uuid := '01935000-0000-7000-8000-000000000601';
  minh constant uuid := '01935000-0000-7000-8000-000000000602';
  khang constant uuid := '01935000-0000-7000-8000-000001100601';
  admin constant uuid := '01935000-0000-7000-8000-0000000000a1';
  bad text;
  found integer;
BEGIN
  -- A grant names a live resource of another teacher, enabled, and is given by its owner.
  SELECT string_agg(g.id::text, ', ') INTO bad
    FROM app.content_shares g
    LEFT JOIN app.tests t ON t.id = g.test_id AND t.deleted_at IS NULL
    LEFT JOIN app.question_sets s ON s.id = g.question_set_id AND s.deleted_at IS NULL
    JOIN app.users u ON u.id = g.grantee_id
    JOIN app.roles r ON r.id = u.role_id
   WHERE g.id::text LIKE mine
     AND (coalesce(t.owner_id, s.owner_id) IS NULL
          OR coalesce(t.owner_id, s.owner_id) = g.grantee_id
          OR g.granted_by <> coalesce(t.owner_id, s.owner_id)
          OR r.builtin_key IS DISTINCT FROM 'teacher' OR u.disabled_at IS NOT NULL);
  IF bad IS NOT NULL THEN
    RAISE EXCEPTION 'seed: grant(s) not as the share command writes them: %', bad;
  END IF;

  -- Both ways between Lan and R2's second teacher: SHARE_IN (a test and a set to Lan, Can
  -- edit and Can use) and SHARE_0 (Lan's test and set to Minh).
  SELECT count(DISTINCT g.kind || ':' || g.access) INTO found
    FROM app.content_shares g
    LEFT JOIN app.tests t ON t.id = g.test_id
    LEFT JOIN app.question_sets s ON s.id = g.question_set_id
   WHERE g.id::text LIKE mine AND g.grantee_id = lan AND coalesce(t.owner_id, s.owner_id) = minh;
  IF found < 2 OR NOT EXISTS (
       SELECT 1 FROM app.content_shares g
         LEFT JOIN app.tests t ON t.id = g.test_id
         LEFT JOIN app.question_sets s ON s.id = g.question_set_id
        WHERE g.id::text LIKE mine AND g.grantee_id = minh AND coalesce(t.owner_id, s.owner_id) = lan AND g.kind = 'test')
     OR NOT EXISTS (
       SELECT 1 FROM app.content_shares g JOIN app.question_sets s ON s.id = g.question_set_id
        WHERE g.id::text LIKE mine AND g.grantee_id = minh AND s.owner_id = lan) THEN
    RAISE EXCEPTION 'seed: Lan and Minh do not share a test and a question set both ways';
  END IF;

  -- A set holds its owner's live standalone questions, and is not empty.
  SELECT string_agg(DISTINCT s.id::text, ', ') INTO bad
    FROM app.question_sets s
    LEFT JOIN app.question_set_items i ON i.set_id = s.id
    LEFT JOIN app.questions q ON q.id = i.question_id
   WHERE s.id::text LIKE mine
     AND (i.set_id IS NULL OR q.owner_id <> s.owner_id OR q.context_group_id IS NOT NULL OR q.deleted_at IS NOT NULL);
  IF bad IS NOT NULL THEN
    RAISE EXCEPTION 'seed: question set(s) empty or holding what is not the owner''s standalone question: %', bad;
  END IF;

  -- The round trip: a published test of Minh's that nobody shares, holding a question
  -- another of his tests' drafts holds, so a co-editor's edit of it forks.
  SELECT count(*) INTO found
    FROM app.tests t
   WHERE t.id::text LIKE mine AND t.owner_id = minh AND t.status = 'published' AND t.deleted_at IS NULL
     AND NOT EXISTS (SELECT 1 FROM app.content_shares g WHERE g.test_id = t.id)
     AND EXISTS (SELECT 1 FROM app.test_sections s
                   JOIN app.test_section_questions m ON m.test_section_id = s.id
                   JOIN app.test_section_questions other ON other.question_id = m.question_id
                   JOIN app.test_sections os ON os.id = other.test_section_id AND os.test_id <> t.id
                   JOIN app.tests ot ON ot.id = os.test_id AND ot.deleted_at IS NULL AND ot.owner_id = minh
                  WHERE s.test_id = t.id);
  IF found <> 1 THEN
    RAISE EXCEPTION 'seed: expected one unshared published test of Minh''s holding a question another test uses, found %', found;
  END IF;

  -- A thread is shaped as the first post makes it: a direct thread's pair is tied and only
  -- they write in it, a channel only its class's teacher; last_message_at is its newest
  -- post; ids order its posts as their times do.
  SELECT string_agg(v.id::text, ', ') INTO bad
    FROM app.conversations v
    LEFT JOIN app.classes c ON c.id = v.class_id
   WHERE v.id::text LIKE mine
     AND ((v.kind = 'direct' AND NOT EXISTS (SELECT 1 FROM app.messaging_ties t
                                              WHERE t.staff_id = v.staff_id AND t.student_id = v.student_id))
          OR EXISTS (SELECT 1 FROM app.messages m
                      WHERE m.conversation_id = v.id
                        AND m.sender_id <> ALL (CASE v.kind WHEN 'direct' THEN ARRAY[v.staff_id, v.student_id]
                                                            ELSE ARRAY[c.teacher_id] END))
          OR v.last_message_at IS DISTINCT FROM (SELECT max(m.created_at) FROM app.messages m WHERE m.conversation_id = v.id)
          OR EXISTS (SELECT 1 FROM app.messages a JOIN app.messages b ON b.conversation_id = a.conversation_id
                      WHERE a.conversation_id = v.id AND a.id < b.id AND a.created_at >= b.created_at));
  IF bad IS NOT NULL THEN
    RAISE EXCEPTION 'seed: thread(s) not shaped as posting makes them: %', bad;
  END IF;

  -- A read pointer names a message of its thread, read by a participant or a member of
  -- the class; and in 08's roster, by someone who has signed in.
  SELECT string_agg(r.conversation_id::text || '/' || r.user_id::text, ', ') INTO bad
    FROM app.conversation_reads r
    JOIN app.conversations v ON v.id = r.conversation_id
    JOIN app.users u ON u.id = r.user_id
   WHERE v.id::text LIKE mine
     AND (NOT EXISTS (SELECT 1 FROM app.messages m WHERE m.id = r.last_read_message_id AND m.conversation_id = v.id)
          OR (v.kind = 'direct' AND r.user_id <> ALL (ARRAY[v.staff_id, v.student_id]))
          OR (v.kind = 'class' AND NOT EXISTS (SELECT 1 FROM app.class_members cm WHERE cm.class_id = v.class_id AND cm.user_id = r.user_id)
                               AND NOT EXISTS (SELECT 1 FROM app.classes c WHERE c.id = v.class_id AND c.teacher_id = r.user_id))
          OR (u.id::text LIKE '01935000-0000-7000-8000-00000080%' AND u.last_sign_in_at IS NULL));
  IF bad IS NOT NULL THEN
    RAISE EXCEPTION 'seed: read pointer(s) no reader could have left: %', bad;
  END IF;

  -- The Teacher deck's CONVS: Lan's unread direct messages are Nam's two and Thu Hà's one.
  SELECT string_agg(u.full_name || '=' || n.unread, ', ' ORDER BY u.full_name) INTO bad
    FROM app.conversations v
    JOIN app.users u ON u.id = v.student_id
    CROSS JOIN LATERAL (
      SELECT count(*) AS unread FROM app.messages m
       WHERE m.conversation_id = v.id AND m.sender_id <> lan
         AND m.id > coalesce((SELECT r.last_read_message_id FROM app.conversation_reads r
                               WHERE r.conversation_id = v.id AND r.user_id = lan), '00000000-0000-0000-0000-000000000000'::uuid)) n
   WHERE v.id::text LIKE mine AND v.kind = 'direct' AND v.staff_id = lan AND n.unread > 0;
  IF bad IS DISTINCT FROM 'Lê Hoàng Nam=2, Phạm Thu Hà=1' THEN
    RAISE EXCEPTION 'seed: expected Lan''s unread to be Nam''s two and Thu Hà''s one, found %', bad;
  END IF;

  -- Its MSGS' "Seen by": every announcement seen by some of its class but not all.
  SELECT string_agg(m.id::text, ', ') INTO bad
    FROM app.messages m
    JOIN app.conversations v ON v.id = m.conversation_id AND v.kind = 'class' AND v.class_id <> '01935000-0000-7000-8000-000000000621'
    CROSS JOIN LATERAL (
      SELECT count(*) FILTER (WHERE r.last_read_message_id >= m.id) AS seen, count(*) AS students
        FROM app.class_members cm
        LEFT JOIN app.conversation_reads r ON r.conversation_id = v.id AND r.user_id = cm.user_id
       WHERE cm.class_id = v.class_id) s
   WHERE v.id::text LIKE mine AND (s.seen = 0 OR s.seen >= s.students);
  IF bad IS NOT NULL THEN
    RAISE EXCEPTION 'seed: announcement(s) seen by none or all of their class: %', bad;
  END IF;

  -- The context card: Nam's thread with Lan starts within a day of his flagged attempt on
  -- her assignment (ContextWindow).
  IF NOT EXISTS (
       SELECT 1 FROM app.conversations v
         JOIN app.attempts a ON a.student_id = v.student_id AND a.flagged AND a.focus_loss_count > 0
         JOIN app.assignments asg ON asg.id = a.assignment_id AND asg.created_by = v.staff_id
        WHERE v.id::text LIKE mine AND v.kind = 'direct' AND v.staff_id = lan
          AND coalesce(a.submitted_at, a.started_at)
              >= (SELECT min(m.created_at) FROM app.messages m WHERE m.conversation_id = v.id) - interval '24 hours') THEN
    RAISE EXCEPTION 'seed: no thread of Lan''s whose context card shows a flagged attempt';
  END IF;

  -- The live specs' pair: Khang alone in Minh's class, with an announcement there and a
  -- direct thread with Minh, nothing unread on either side, and no peer of the Admin's
  -- (visibility.MessagingPeerIDs), so the Admin's token finds no thread with him.
  SELECT count(*) INTO found FROM app.class_members WHERE class_id = '01935000-0000-7000-8000-000000000621';
  IF found <> 1 OR NOT EXISTS (SELECT 1 FROM app.class_members
                                WHERE class_id = '01935000-0000-7000-8000-000000000621' AND user_id = khang) THEN
    RAISE EXCEPTION 'seed: expected Khang to be the only member of Minh''s class, found % members', found;
  END IF;
  IF EXISTS (SELECT 1 FROM app.conversations v WHERE v.kind = 'direct' AND v.staff_id = admin AND v.student_id = khang)
     OR EXISTS (SELECT 1 FROM app.messaging_ties t WHERE t.staff_id = admin AND t.student_id = khang)
     OR EXISTS (SELECT 1 FROM app.users u WHERE u.id = khang AND u.created_by = admin) THEN
    RAISE EXCEPTION 'seed: Khang is a peer of the Admin''s, so the Admin''s token would find a thread with him';
  END IF;
  SELECT string_agg(v.kind, ', ' ORDER BY v.kind) INTO bad
    FROM app.conversations v
   WHERE v.id::text LIKE mine
     AND (v.class_id = '01935000-0000-7000-8000-000000000621' OR (v.staff_id = minh AND v.student_id = khang))
     AND EXISTS (SELECT 1 FROM app.messages m WHERE m.conversation_id = v.id)
     AND NOT EXISTS (
       SELECT 1 FROM unnest(CASE v.kind WHEN 'direct' THEN ARRAY[minh, khang] ELSE ARRAY[khang] END) AS reader(id)
        WHERE EXISTS (SELECT 1 FROM app.messages m
                       WHERE m.conversation_id = v.id AND m.sender_id <> reader.id
                         AND m.id > coalesce((SELECT r.last_read_message_id FROM app.conversation_reads r
                                               WHERE r.conversation_id = v.id AND r.user_id = reader.id),
                                             '00000000-0000-0000-0000-000000000000'::uuid)));
  IF bad IS DISTINCT FROM 'class, direct' THEN
    RAISE EXCEPTION 'seed: expected Minh''s channel and his thread with Khang, posted in and read, found %', bad;
  END IF;

  RAISE NOTICE 'seed: collaboration holds';
END $$;

-- 12's flexible assignment (F1, T-F1.9): flexible, published, reaching 01's student through
-- the class, with fullscreen off for headless Chromium; open now, which a database seeded more
-- than thirty days ago is not, since 12 inserts with DO NOTHING; holding the strict rules the
-- live spec counts on flexible not to apply (one minute, a submit on the second absence); and
-- the live fixtures of 04 and 10, whose specs test strict, still strict.
DO $$
DECLARE
  flexible constant uuid := '01935000-0000-7000-8000-000000001201';
  student constant uuid := '01935000-0000-7000-8000-0000000000a2';
  bad text;
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM app.assignments a
     WHERE a.id = flexible AND a.mode = 'flexible' AND a.published_at IS NOT NULL
       AND a.closed_at IS NULL AND NOT a.integrity_require_fullscreen AND a.max_attempts > 1
       AND EXISTS (SELECT 1 FROM app.assignment_classes ac
                     JOIN app.class_members m ON m.class_id = ac.class_id
                    WHERE ac.assignment_id = a.id AND m.user_id = student)) THEN
    RAISE EXCEPTION 'seed: no published flexible assignment %, fullscreen off, reaching hocvien@quizzivy.com', flexible;
  END IF;

  IF NOT EXISTS (
    SELECT 1 FROM app.assignments a
     WHERE a.id = flexible AND a.opens_at <= now() AND a.closes_at > now()) THEN
    RAISE EXCEPTION 'seed: flexible assignment % is not open now (seeded more than thirty days ago? reset the database and seed again)', flexible;
  END IF;

  IF NOT EXISTS (
    SELECT 1 FROM app.assignments a
     WHERE a.id = flexible AND a.duration_minutes = 1 AND a.integrity_max_focus_loss = 1
       AND a.integrity_on_limit_exceeded = 'auto_submit') THEN
    RAISE EXCEPTION 'seed: flexible assignment % no longer stores the strict rules its live spec relies on: a one-minute limit and a submit on the second absence', flexible;
  END IF;

  SELECT string_agg(a.id::text, ', ') INTO bad
    FROM app.assignments a
   WHERE a.mode <> 'strict'
     AND (a.id::text LIKE '01935000-0000-7000-8000-00000000ee%'
          OR a.id::text LIKE '01935000-0000-7000-8000-0000000010%');
  IF bad IS NOT NULL THEN
    RAISE EXCEPTION 'seed: live strict fixture(s) not strict: %', bad;
  END IF;

  RAISE NOTICE 'seed: flexible assignment holds';
END $$;
