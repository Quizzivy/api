package repositories

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"quizzivy/internal/modules/questions/domain"
	"quizzivy/internal/shared/audit"
	"quizzivy/internal/shared/content"
	"quizzivy/internal/shared/opt"
)

// CopyQuestions copies the live standalone bank questions in.IDs (domain.Copy)
// inside the caller's transaction tx, and answers the copies in the order of
// in.IDs: an id given twice is copied once and answered twice. It copies the
// rows as they are stored, as restoring a version as a draft copies a frozen
// question (no validation of today's rules, which a stored question may
// predate): the question, its options, blanks and accepted answers, match
// items keyed to the copy's options by place, rubric criteria and the media
// its prose shows, then gives the copy's gaps fresh ids. Copies reference the
// sources' media by asset id, since assets are immutable.
//
// Locks go question, then media, the order a question write takes them: every
// source is read under its row's share lock, so no edit lands halfway through
// its copy, then each asset the sources name is locked with
// media.LockForVersionUse in id order before any copy names it, so a
// concurrent deleteMedia either sees the copy or makes the copy fail. A
// source the reader cannot read, or that is missing, deleted or a group
// member, is a QuestionsNotFoundError naming them in the order given; an
// asset deleted since its source bound it is ErrMediaNotFound.
func CopyQuestions(ctx context.Context, tx pgx.Tx, media MediaLocks, in domain.Copy) ([]string, error) {
	sources := distinctIDs(in.IDs)
	if len(sources) == 0 {
		return []string{}, nil
	}
	if err := lockCopySources(ctx, tx, in, sources); err != nil {
		return nil, err
	}
	if err := lockCopyMedia(ctx, tx, media, sources); err != nil {
		return nil, err
	}
	copies := make(map[string]string, len(sources))
	for _, source := range sources {
		id, err := copyBankQuestion(ctx, tx, source, in)
		if err != nil {
			return nil, err
		}
		copies[source] = id
	}
	out := make([]string, len(in.IDs))
	for i, id := range in.IDs {
		out[i] = copies[strings.ToLower(id)]
	}
	return out, nil
}

func distinctIDs(ids []string) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if id = strings.ToLower(id); !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out
}

// lockCopySources takes the share lock of every source the reader can read,
// in id order, and refuses the others as missing ones.
func lockCopySources(ctx context.Context, tx pgx.Tx, in domain.Copy, sources []string) error {
	rows, err := tx.Query(ctx, `SELECT q.id::text FROM app.questions q
		 WHERE q.id = ANY($1::uuid[]) AND q.context_group_id IS NULL AND q.deleted_at IS NULL
		   AND `+questionAccess(2, 3)+` IS NOT NULL
		 ORDER BY q.id FOR SHARE OF q`, sources, in.Reader.All, opt.String(in.Reader.UserID))
	if err != nil {
		return fmt.Errorf("questions: lock copy sources: %w", err)
	}
	locked, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return fmt.Errorf("questions: lock copy sources: %w", err)
	}
	var missing []string
	for _, id := range sources {
		if !slices.Contains(locked, id) {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		return &domain.QuestionsNotFoundError{IDs: missing}
	}
	return nil
}

// lockCopyMedia locks, in id order, every asset the sources attach or show.
func lockCopyMedia(ctx context.Context, tx pgx.Tx, media MediaLocks, sources []string) error {
	rows, err := tx.Query(ctx, `SELECT id::text FROM (
		  SELECT media_asset_id AS id FROM app.questions WHERE id = ANY($1::uuid[]) AND media_asset_id IS NOT NULL
		  UNION
		  SELECT media_asset_id FROM app.question_media_references WHERE question_id = ANY($1::uuid[])
		) assets ORDER BY id`, sources)
	if err != nil {
		return fmt.Errorf("questions: copy media: %w", err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return fmt.Errorf("questions: copy media: %w", err)
	}
	if len(ids) > 0 && media == nil {
		return errors.New("questions: media locks unavailable")
	}
	for _, id := range ids {
		if err := media.LockForVersionUse(ctx, tx, id); err != nil {
			return err
		}
	}
	return nil
}

// copyBankQuestion copies the locked source as a new bank question of
// in.OwnerID's, created by in.ActorID, and audits it.
func copyBankQuestion(ctx context.Context, tx pgx.Tx, source string, in domain.Copy) (string, error) {
	var id string
	if err := tx.QueryRow(ctx, `INSERT INTO app.questions
		  (type, prompt, media_asset_id, media_asset_kind, audio_max_plays, audio_allow_seek,
		   audio_show_transcript_after, transcript, points, explanation, sample_answer, tags, created_by, updated_by,
		   prompt_content, explanation_content, owner_id, level, skill, media_alt, audio_allow_pause,
		   select_count, max_words, match_allow_reuse)
		SELECT q.type, q.prompt, q.media_asset_id, q.media_asset_kind, q.audio_max_plays, q.audio_allow_seek,
		   q.audio_show_transcript_after, q.transcript, q.points, q.explanation, q.sample_answer, q.tags, $2::uuid, $2::uuid,
		   q.prompt_content, q.explanation_content, $3::uuid, q.level, q.skill, q.media_alt, q.audio_allow_pause,
		   q.select_count, q.max_words, q.match_allow_reuse
		  FROM app.questions q WHERE q.id = $1::uuid
		RETURNING id::text`, source, in.ActorID, in.OwnerID).Scan(&id); err != nil {
		return "", writeFailure(err)
	}
	if err := copyQuestionChildren(ctx, tx, source, id); err != nil {
		return "", err
	}
	if err := rebindCopiedGaps(ctx, tx, id); err != nil {
		return "", err
	}
	entries := []audit.Entry{{Action: "question.created"}}
	if in.AuditEach {
		diff, err := json.Marshal(map[string]string{"sourceId": source})
		if err != nil {
			return "", err
		}
		entries = append(entries, audit.Entry{Action: "share.copied", Diff: diff})
	}
	for _, entry := range entries {
		entry.ActorUserID, entry.Entity, entry.EntityID = &in.ActorID, entityQuestion, &id
		entry.OccurredAt, entry.IP, entry.UserAgent = in.Now, opt.String(in.IP), opt.String(in.UserAgent)
		if err := audit.Write(ctx, tx, entry); err != nil {
			return "", err
		}
	}
	return id, nil
}

// copyQuestionChildren copies source's options, blanks with their accepted
// answers, match items (keyed to the copy's option in the same place), rubric
// criteria and prose media references onto the copy id.
func copyQuestionChildren(ctx context.Context, tx pgx.Tx, source, id string) error {
	for _, statement := range []string{
		`INSERT INTO app.question_options (question_id, ordinal, text, is_correct, content)
		 SELECT $2::uuid, ordinal, text, is_correct, content FROM app.question_options WHERE question_id = $1::uuid`,
		`WITH copied AS (
		   INSERT INTO app.question_blanks (question_id, ordinal, case_sensitive, gap_id)
		   SELECT $2::uuid, ordinal, case_sensitive, gap_id FROM app.question_blanks WHERE question_id = $1::uuid
		   RETURNING id, ordinal
		 )
		 INSERT INTO app.question_blank_answers (blank_id, answer)
		 SELECT copied.id, a.answer FROM copied
		   JOIN app.question_blanks b ON b.question_id = $1::uuid AND b.ordinal = copied.ordinal
		   JOIN app.question_blank_answers a ON a.blank_id = b.id`,
		`INSERT INTO app.question_match_items (question_id, ordinal, text, correct_option_id)
		 SELECT $2::uuid, i.ordinal, i.text, o.id
		   FROM app.question_match_items i
		   JOIN app.question_options source ON source.id = i.correct_option_id
		   JOIN app.question_options o ON o.question_id = $2::uuid AND o.ordinal = source.ordinal
		  WHERE i.question_id = $1::uuid`,
		`INSERT INTO app.question_rubric_criteria (question_id, ordinal, label)
		 SELECT $2::uuid, ordinal, label FROM app.question_rubric_criteria WHERE question_id = $1::uuid`,
		`INSERT INTO app.question_media_references (question_id, field, media_asset_id, media_asset_kind)
		 SELECT $2::uuid, field, media_asset_id, media_asset_kind FROM app.question_media_references WHERE question_id = $1::uuid`,
	} {
		if _, err := tx.Exec(ctx, statement, source, id); err != nil {
			return fmt.Errorf("questions: copy children: %w", err)
		}
	}
	return nil
}

// rebindCopiedGaps gives the copy questionID's gaps fresh ids, in its prompt
// and on its blanks alike, so no two questions share a gap id.
func rebindCopiedGaps(ctx context.Context, tx pgx.Tx, questionID string) error {
	var raw json.RawMessage
	if err := tx.QueryRow(ctx, `SELECT prompt_content FROM app.questions WHERE id = $1::uuid`, questionID).Scan(&raw); err != nil {
		return fmt.Errorf("questions: copy gaps: %w", err)
	}
	if len(raw) == 0 {
		return nil
	}
	document, err := content.ParsePromptWithMedia(raw)
	if err != nil || len(document.GapIDs()) == 0 {
		return err
	}
	oldIDs := document.GapIDs()
	newIDs := make([]string, len(oldIDs))
	ids := make(map[string]string, len(oldIDs))
	for i, id := range oldIDs {
		newIDs[i] = uuid.NewString()
		ids[id] = newIDs[i]
	}
	copied, err := document.WithGapIDs(ids)
	if err != nil {
		return err
	}
	encoded, err := copied.MarshalJSON()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE app.question_blanks b SET gap_id = m.new_id
		  FROM unnest($2::text[], $3::text[]) AS m(old_id, new_id)
		 WHERE b.question_id = $1::uuid AND b.gap_id = m.old_id`, questionID, oldIDs, newIDs); err != nil {
		return fmt.Errorf("questions: copy gaps: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE app.questions SET prompt_content = $2 WHERE id = $1::uuid`, questionID, encoded); err != nil {
		return fmt.Errorf("questions: copy gaps: %w", err)
	}
	return nil
}

// CopySet copies the set w.ID, which the actor must be able to read, into the
// actor's own content (copyQuestionSet, T-R7.7). Without questionIDs it makes
// a new set owned by the actor, of the same name and description, holding a
// copy of each of its questions in set order, audited as question_set.created
// and share.copied from the source set. With questionIDs, each a member of the
// set, it copies just those into the actor's bank and makes no set, each copy
// audited as share.copied from its question. The answer lists the copies in
// set order. A set the actor cannot read is ErrSetNotFound; an id it does not
// hold a QuestionsNotFoundError naming those, in the order given.
//
// Locks go set, then question, then media, as an edit in a set's context
// takes them; each copy is locked with LockForDraftUse before an item names
// it, as every insert into question_set_items is.
func (s *Postgres) CopySet(ctx context.Context, w domain.SetWrite, questionIDs []string) (domain.SetCopy, error) {
	if s.media == nil {
		return domain.SetCopy{}, errors.New("questions: media locks unavailable")
	}
	tx, err := s.Begin(ctx)
	if err != nil {
		return domain.SetCopy{}, fmt.Errorf("questions: begin copy set: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var name string
	var description *string
	err = tx.QueryRow(ctx, `SELECT s.name, s.description FROM app.question_sets s
		 WHERE s.id = $1::uuid AND s.deleted_at IS NULL AND `+SetAccess(2, 3)+` IS NOT NULL
		 FOR SHARE OF s`, w.ID, w.All, opt.String(w.ActorID)).Scan(&name, &description)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.SetCopy{}, domain.ErrSetNotFound
	}
	if err != nil {
		return domain.SetCopy{}, fmt.Errorf("questions: read copied set: %w", err)
	}
	rows, err := tx.Query(ctx, `SELECT question_id::text FROM app.question_set_items WHERE set_id = $1::uuid ORDER BY ordinal`, w.ID)
	if err != nil {
		return domain.SetCopy{}, fmt.Errorf("questions: copied set items: %w", err)
	}
	held, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return domain.SetCopy{}, fmt.Errorf("questions: copied set items: %w", err)
	}
	sources, err := setCopySources(held, questionIDs)
	if err != nil {
		return domain.SetCopy{}, err
	}
	partial := questionIDs != nil
	copies, err := CopyQuestions(ctx, tx, s.media, domain.Copy{IDs: sources, Reader: w.Scope(), OwnerID: w.ActorID, ActorID: w.ActorID,
		AuditEach: partial, Now: w.Now, IP: w.IP, UserAgent: w.UserAgent})
	if err != nil {
		return domain.SetCopy{}, err
	}
	out := domain.SetCopy{QuestionIDs: copies}
	if !partial {
		id, err := insertCopiedSet(ctx, tx, w, name, description, copies)
		if err != nil {
			return domain.SetCopy{}, err
		}
		out.SetID = &id
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.SetCopy{}, fmt.Errorf("questions: commit copy set: %w", err)
	}
	return out, nil
}

// setCopySources is what a set copy copies, in set order: every question the
// set holds when chosen is nil, and otherwise the chosen ones, each of which
// the set must hold.
func setCopySources(held, chosen []string) ([]string, error) {
	if chosen == nil {
		return held, nil
	}
	var missing []string
	for _, id := range chosen {
		if !slices.Contains(held, strings.ToLower(id)) {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		return nil, &domain.QuestionsNotFoundError{IDs: missing}
	}
	var sources []string
	for _, id := range held {
		if slices.ContainsFunc(chosen, func(c string) bool { return strings.EqualFold(c, id) }) {
			sources = append(sources, id)
		}
	}
	return sources, nil
}

// insertCopiedSet makes the actor's copy of a set, named and described as
// the source, holding copies in order, and audits it as created and as copied
// from the source set w.ID.
func insertCopiedSet(ctx context.Context, tx pgx.Tx, w domain.SetWrite, name string, description *string, copies []string) (string, error) {
	for _, id := range copies {
		if err := LockForDraftUse(ctx, tx, id); err != nil {
			return "", err
		}
	}
	var id string
	if err := tx.QueryRow(ctx, `
		WITH created AS (
		  INSERT INTO app.question_sets (owner_id, name, description, created_by, updated_by)
		  VALUES ($1::uuid, $2, $3, $1::uuid, $1::uuid)
		  RETURNING id
		), items AS (
		  INSERT INTO app.question_set_items (set_id, question_id, ordinal, added_by)
		  SELECT created.id, t.question_id, t.ordinal - 1, $1::uuid
		    FROM created, unnest($4::uuid[]) WITH ORDINALITY AS t(question_id, ordinal)
		), logged AS (
		  INSERT INTO app.audit_log (actor_user_id, action, entity, entity_id, occurred_at, ip, user_agent, diff)
		  SELECT $1::uuid, 'question_set.created', 'question_set', created.id, $5::timestamptz, $6::inet, $7::text,
		         jsonb_build_object('added', to_jsonb($4::uuid[]))
		    FROM created
		  UNION ALL
		  SELECT $1::uuid, 'share.copied', 'question_set', created.id, $5::timestamptz, $6::inet, $7::text,
		         jsonb_build_object('sourceId', $8::uuid)
		    FROM created
		)
		SELECT id::text FROM created`,
		w.ActorID, name, description, copies, w.Now, opt.String(w.IP), opt.String(w.UserAgent), w.ID).Scan(&id); err != nil {
		return "", fmt.Errorf("questions: copy set: %w", err)
	}
	return id, nil
}
