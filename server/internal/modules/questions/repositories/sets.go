package repositories

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5"

	"quizzivy/internal/modules/questions/domain"
	"quizzivy/internal/platform/db"
	"quizzivy/internal/shared/access"
	"quizzivy/internal/shared/opt"
)

const personName = `coalesce(nullif(u.display_name, ''), u.full_name)`

func setColumns(all, user int) string {
	return `s.id::text, s.owner_id::text, ` + personName + `, s.name, s.description, ` + SetAccess(all, user) + `,
	       (SELECT count(*) FROM app.question_set_items i JOIN app.questions q ON q.id = i.question_id AND q.deleted_at IS NULL
	         WHERE i.set_id = s.id),
	       (SELECT count(*) FROM app.content_shares g WHERE g.question_set_id = s.id),
	       s.created_at, s.updated_at
	  FROM app.question_sets s JOIN app.users u ON u.id = s.owner_id`
}

func scanSet(row pgx.Row) (domain.QuestionSet, error) {
	var set domain.QuestionSet
	var access *string
	err := row.Scan(&set.ID, &set.OwnerID, &set.OwnerName, &set.Name, &set.Description, &access,
		&set.QuestionCount, &set.SharedWith, &set.CreatedAt, &set.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && access == nil {
		return domain.QuestionSet{}, domain.ErrSetNotFound
	}
	if err != nil {
		return domain.QuestionSet{}, fmt.Errorf("questions: scan set: %w", err)
	}
	set.Access = domain.Access(*access)
	if set.Access != domain.AccessOwner {
		set.SharedWith = 0
	}
	return set, nil
}

// ListSets returns the live sets scope.UserID owns, most recently changed
// first. A set shared with the caller is listed by Shared with me instead.
func (s *Postgres) ListSets(ctx context.Context, scope access.Scope) ([]domain.QuestionSet, error) {
	sets, err := db.QueryMany(ctx, s, `SELECT `+setColumns(2, 1)+`
		 WHERE s.owner_id = $1::uuid AND s.deleted_at IS NULL
		 ORDER BY s.updated_at DESC, s.id DESC`, []any{opt.String(scope.UserID), false}, func(rows pgx.Rows) (domain.QuestionSet, error) {
		return scanSet(rows)
	})
	if err != nil {
		return nil, fmt.Errorf("questions: list sets: %w", err)
	}
	return sets, nil
}

// GetSet returns a live set the scope can read; anything else, another
// teacher's set included, is ErrSetNotFound.
func (s *Postgres) GetSet(ctx context.Context, scope access.Scope, id string) (domain.QuestionSet, error) {
	return getSet(ctx, s, scope, id)
}

func getSet(ctx context.Context, q db.Querier, scope access.Scope, id string) (domain.QuestionSet, error) {
	return scanSet(q.QueryRow(ctx, `SELECT `+setColumns(2, 3)+`
		 WHERE s.id = $1::uuid AND s.deleted_at IS NULL`, id, scope.All, opt.String(scope.UserID)))
}

func lockSet(ctx context.Context, tx pgx.Tx, w domain.SetWrite, action domain.SetAction) (string, error) {
	var owner, granted string
	err := tx.QueryRow(ctx, `SELECT s.owner_id::text, `+SetAccess(2, 3)+` FROM app.question_sets s
		 WHERE s.id = $1::uuid AND s.deleted_at IS NULL AND `+SetAccess(2, 3)+` IS NOT NULL
		 FOR UPDATE OF s`, w.ID, w.All, opt.String(w.ActorID)).Scan(&owner, &granted)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", domain.ErrSetNotFound
	}
	if err != nil {
		return "", fmt.Errorf("questions: lock set: %w", err)
	}
	return owner, domain.Access(granted).Allows(action)
}

func lockBankQuestions(ctx context.Context, tx pgx.Tx, owner string, ids []string) ([]string, error) {
	rows, err := tx.Query(ctx, `SELECT id::text FROM app.questions
		 WHERE id = ANY($1::uuid[]) AND owner_id = $2::uuid AND context_group_id IS NULL AND deleted_at IS NULL
		 ORDER BY id`, ids, owner)
	if err != nil {
		return nil, fmt.Errorf("questions: set candidates: %w", err)
	}
	candidates, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("questions: set candidates: %w", err)
	}
	locked := make(map[string]bool, len(candidates))
	for _, id := range candidates {
		switch err := LockForDraftUse(ctx, tx, id); {
		case errors.Is(err, domain.ErrNotFound):
		case err != nil:
			return nil, err
		default:
			locked[id] = true
		}
	}
	strays, err := NotOwnedBy(ctx, tx, owner, candidates)
	if err != nil {
		return nil, err
	}
	for _, id := range strays {
		delete(locked, id)
	}
	var missing []string
	for _, id := range ids {
		if !locked[id] {
			missing = append(missing, id)
		}
	}
	return missing, nil
}

// CreateSet makes a set owned by the actor, holding questionIDs in the order
// given, and audits it in the same statement. Every id must be a live
// standalone question of the actor's bank; otherwise nothing is made and the
// error names the others.
func (s *Postgres) CreateSet(ctx context.Context, w domain.SetWrite, fields domain.SetFields, questionIDs []string) (domain.QuestionSet, error) {
	tx, err := s.Begin(ctx)
	if err != nil {
		return domain.QuestionSet{}, fmt.Errorf("questions: begin create set: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	missing, err := lockBankQuestions(ctx, tx, w.ActorID, questionIDs)
	if err != nil {
		return domain.QuestionSet{}, err
	}
	if len(missing) > 0 {
		return domain.QuestionSet{}, &domain.QuestionsNotFoundError{IDs: missing}
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
		  SELECT $1::uuid, 'question_set.created', 'question_set', created.id, $5, $6::inet, $7,
		         jsonb_build_object('added', to_jsonb($4::uuid[]))
		    FROM created
		)
		SELECT id::text FROM created`,
		w.ActorID, *fields.Name, fields.Description, questionIDs, w.Now, opt.String(w.IP), opt.String(w.UserAgent)).Scan(&id); err != nil {
		return domain.QuestionSet{}, fmt.Errorf("questions: create set: %w", err)
	}
	return commitSet(ctx, tx, w, id)
}

// UpdateSet changes the set's name or description for its owner or an edit
// grantee, and audits what changed in the same statement as an {old, new}
// pair per field, with a null for a description that was absent or cleared.
// A change that leaves both as they are writes nothing.
func (s *Postgres) UpdateSet(ctx context.Context, w domain.SetWrite, fields domain.SetFields) (domain.QuestionSet, error) {
	tx, err := s.Begin(ctx)
	if err != nil {
		return domain.QuestionSet{}, fmt.Errorf("questions: begin update set: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := lockSet(ctx, tx, w, domain.SetChange); err != nil {
		return domain.QuestionSet{}, err
	}
	if _, err := tx.Exec(ctx, `
		WITH updated AS (
		  UPDATE app.question_sets s
		     SET name = coalesce($2::text, s.name),
		         description = CASE WHEN $3::boolean THEN $4::text ELSE s.description END,
		         updated_by = $5::uuid
		   WHERE s.id = $1::uuid
		     AND (($2::text IS NOT NULL AND $2::text <> s.name) OR ($3::boolean AND $4::text IS DISTINCT FROM s.description))
		  RETURNING s.id, old.name AS old_name, new.name AS new_name,
		            old.description AS old_description, new.description AS new_description
		)
		INSERT INTO app.audit_log (actor_user_id, action, entity, entity_id, occurred_at, ip, user_agent, diff)
		SELECT $5::uuid, 'question_set.updated', 'question_set', updated.id, $6, $7::inet, $8,
		       jsonb_build_object('changed',
		         CASE WHEN updated.old_name <> updated.new_name
		              THEN jsonb_build_object('name', jsonb_build_object('old', updated.old_name, 'new', updated.new_name))
		              ELSE '{}'::jsonb END
		         || CASE WHEN updated.old_description IS DISTINCT FROM updated.new_description
		                 THEN jsonb_build_object('description', jsonb_build_object('old', updated.old_description, 'new', updated.new_description))
		                 ELSE '{}'::jsonb END)
		  FROM updated`,
		w.ID, fields.Name, fields.DescriptionSet, fields.Description, w.ActorID, w.Now, opt.String(w.IP), opt.String(w.UserAgent)); err != nil {
		return domain.QuestionSet{}, fmt.Errorf("questions: update set: %w", err)
	}
	return commitSet(ctx, tx, w, w.ID)
}

// DeleteSet soft-deletes the set for its owner, deletes every grant to it and
// audits it, in one statement. Its questions stay in the bank.
func (s *Postgres) DeleteSet(ctx context.Context, w domain.SetWrite) error {
	tx, err := s.Begin(ctx)
	if err != nil {
		return fmt.Errorf("questions: begin delete set: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := lockSet(ctx, tx, w, domain.SetDelete); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		WITH deleted AS (
		  UPDATE app.question_sets SET deleted_at = $2, updated_by = $3::uuid
		   WHERE id = $1::uuid AND deleted_at IS NULL
		  RETURNING id
		), revoked AS (
		  DELETE FROM app.content_shares WHERE question_set_id IN (SELECT id FROM deleted)
		)
		INSERT INTO app.audit_log (actor_user_id, action, entity, entity_id, occurred_at, ip, user_agent)
		SELECT $3::uuid, 'question_set.deleted', 'question_set', deleted.id, $2, $4::inet, $5
		  FROM deleted`,
		w.ID, w.Now, w.ActorID, opt.String(w.IP), opt.String(w.UserAgent)); err != nil {
		return fmt.Errorf("questions: delete set: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("questions: commit delete set: %w", err)
	}
	return nil
}

// AddToSet appends, for the set's owner, the questions of questionIDs it does
// not hold yet, in the order given, and audits them in the same statement.
// Each must be a live standalone question of the owner's bank, locked with
// LockForDraftUse before its item is inserted, so an add and a delete of the
// question contend on its row.
func (s *Postgres) AddToSet(ctx context.Context, w domain.SetWrite, questionIDs []string) (domain.QuestionSet, error) {
	tx, err := s.Begin(ctx)
	if err != nil {
		return domain.QuestionSet{}, fmt.Errorf("questions: begin add to set: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	owner, err := lockSet(ctx, tx, w, domain.SetAdd)
	if err != nil {
		return domain.QuestionSet{}, err
	}
	rows, err := tx.Query(ctx, `SELECT question_id::text FROM app.question_set_items WHERE set_id = $1::uuid`, w.ID)
	if err != nil {
		return domain.QuestionSet{}, fmt.Errorf("questions: set items: %w", err)
	}
	held, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return domain.QuestionSet{}, fmt.Errorf("questions: set items: %w", err)
	}
	var adding []string
	for _, id := range questionIDs {
		if !slices.Contains(held, id) {
			adding = append(adding, id)
		}
	}
	if len(adding) == 0 {
		return commitSet(ctx, tx, w, w.ID)
	}
	missing, err := lockBankQuestions(ctx, tx, owner, adding)
	if err != nil {
		return domain.QuestionSet{}, err
	}
	if len(missing) > 0 {
		return domain.QuestionSet{}, &domain.QuestionsNotFoundError{IDs: missing}
	}
	if err := domain.SetCapacity(len(held), len(adding)); err != nil {
		return domain.QuestionSet{}, err
	}
	if _, err := tx.Exec(ctx, `
		WITH added AS (
		  INSERT INTO app.question_set_items (set_id, question_id, ordinal, added_by)
		  SELECT $1::uuid, t.question_id,
		         (SELECT coalesce(max(i.ordinal), -1) FROM app.question_set_items i WHERE i.set_id = $1::uuid) + t.ordinal,
		         $2::uuid
		    FROM unnest($3::uuid[]) WITH ORDINALITY AS t(question_id, ordinal)
		  RETURNING set_id
		), touched AS (
		  UPDATE app.question_sets SET updated_by = $2::uuid WHERE id = $1::uuid
		)
		INSERT INTO app.audit_log (actor_user_id, action, entity, entity_id, occurred_at, ip, user_agent, diff)
		SELECT $2::uuid, 'question_set.updated', 'question_set', $1::uuid, $4, $5::inet, $6,
		       jsonb_build_object('added', to_jsonb($3::uuid[]))
		 WHERE EXISTS (SELECT 1 FROM added)`,
		w.ID, w.ActorID, adding, w.Now, opt.String(w.IP), opt.String(w.UserAgent)); err != nil {
		return domain.QuestionSet{}, fmt.Errorf("questions: add to set: %w", err)
	}
	return commitSet(ctx, tx, w, w.ID)
}

// RemoveFromSet takes the questions of questionIDs out of the set, for its
// owner or an edit grantee, and audits those it held in the same statement.
// The questions stay in the bank.
func (s *Postgres) RemoveFromSet(ctx context.Context, w domain.SetWrite, questionIDs []string) (domain.QuestionSet, error) {
	tx, err := s.Begin(ctx)
	if err != nil {
		return domain.QuestionSet{}, fmt.Errorf("questions: begin remove from set: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := lockSet(ctx, tx, w, domain.SetChange); err != nil {
		return domain.QuestionSet{}, err
	}
	if _, err := tx.Exec(ctx, `
		WITH removed AS (
		  DELETE FROM app.question_set_items
		   WHERE set_id = $1::uuid AND question_id = ANY($3::uuid[])
		  RETURNING question_id, ordinal
		), touched AS (
		  UPDATE app.question_sets SET updated_by = $2::uuid
		   WHERE id = $1::uuid AND EXISTS (SELECT 1 FROM removed)
		)
		INSERT INTO app.audit_log (actor_user_id, action, entity, entity_id, occurred_at, ip, user_agent, diff)
		SELECT $2::uuid, 'question_set.updated', 'question_set', $1::uuid, $4, $5::inet, $6,
		       jsonb_build_object('removed', jsonb_agg(removed.question_id ORDER BY removed.ordinal))
		  FROM removed
		HAVING count(*) > 0`,
		w.ID, w.ActorID, questionIDs, w.Now, opt.String(w.IP), opt.String(w.UserAgent)); err != nil {
		return domain.QuestionSet{}, fmt.Errorf("questions: remove from set: %w", err)
	}
	return commitSet(ctx, tx, w, w.ID)
}

func commitSet(ctx context.Context, tx pgx.Tx, w domain.SetWrite, id string) (domain.QuestionSet, error) {
	set, err := getSet(ctx, tx, w.Scope(), id)
	if err != nil {
		return domain.QuestionSet{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.QuestionSet{}, fmt.Errorf("questions: commit set: %w", err)
	}
	return set, nil
}

// RemoveFromSets deletes every set item of the question, inside the delete
// that holds the question's row lock.
func RemoveFromSets(ctx context.Context, tx pgx.Tx, questionID string) error {
	if _, err := tx.Exec(ctx, `DELETE FROM app.question_set_items WHERE question_id = $1::uuid`, questionID); err != nil {
		return fmt.Errorf("questions: remove from sets: %w", err)
	}
	return nil
}

// UpdateInSet writes in, an edit of the bank question in.ID made in the
// context of the set setID (updateQuestion?setId), and answers the question as
// saved: the copy when the edit forks it (copy-on-write, D4). The set's row is
// locked first, then the question's (LockForDraftUse), the order autosave and
// add-to-set take. The set must be one the actor can read and must hold the
// question, or the edit answers ErrSetNotFound, as a missing set does; a
// reader who may not change it gets the refusal of Access.Allows. On a fork
// only this set's item is pointed at the copy, which LockForDraftUse locks
// first, and the set records the editor.
func (s *Postgres) UpdateInSet(ctx context.Context, in domain.WriteInput, setID string) (domain.Question, error) {
	tx, err := s.Begin(ctx)
	if err != nil {
		return domain.Question{}, fmt.Errorf("questions: begin set edit: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	_, refused := lockSet(ctx, tx, domain.SetWrite{ID: setID, ActorID: in.ActorID, All: in.All}, domain.SetChange)
	if errors.Is(refused, domain.ErrSetNotFound) || refused != nil && !domain.IsSetRefusal(refused) {
		return domain.Question{}, refused
	}
	held, err := db.Exists(ctx, tx, `SELECT 1 FROM app.question_set_items WHERE set_id = $1::uuid AND question_id = $2::uuid`, setID, in.ID)
	if err != nil {
		return domain.Question{}, fmt.Errorf("questions: set context: %w", err)
	}
	if !held {
		return domain.Question{}, domain.ErrSetNotFound
	}
	if refused != nil {
		return domain.Question{}, refused
	}
	if err := LockForDraftUse(ctx, tx, in.ID); err != nil {
		return domain.Question{}, err
	}
	within := domain.EditContext{Kind: domain.ContextSet, ID: setID}
	use, err := ContextUse(ctx, tx, in.ID, within)
	if err != nil {
		return domain.Question{}, err
	}
	inner := &Postgres{Repository: db.NewRepository(db.NewContext(tx)), media: s.media}
	written, forked, err := inner.WriteInContext(ctx, in, use, within)
	if err != nil {
		return domain.Question{}, err
	}
	if forked {
		if err := repointSetItem(ctx, tx, setID, use.ID, written.ID, in.ActorID); err != nil {
			return domain.Question{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Question{}, fmt.Errorf("questions: commit set edit: %w", err)
	}
	return written, nil
}

// repointSetItem points the set's item for the question from at its fork to,
// in place, so the item keeps its ordinal, and records the editor on the set.
// The fork is locked first, as every insert of a set item locks its question.
func repointSetItem(ctx context.Context, tx pgx.Tx, setID, from, to, actorID string) error {
	if err := LockForDraftUse(ctx, tx, to); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		WITH moved AS (
		  UPDATE app.question_set_items SET question_id = $3::uuid
		   WHERE set_id = $1::uuid AND question_id = $2::uuid
		  RETURNING set_id
		)
		UPDATE app.question_sets SET updated_by = $4::uuid WHERE id IN (SELECT set_id FROM moved)`,
		setID, from, to, actorID); err != nil {
		return fmt.Errorf("questions: repoint set item: %w", err)
	}
	return nil
}
