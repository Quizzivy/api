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
	"quizzivy/internal/platform/db"
	"quizzivy/internal/shared/access"
	"quizzivy/internal/shared/audit"
	"quizzivy/internal/shared/content"
	"quizzivy/internal/shared/opt"
)

// ContextUse reads what the copy-on-write rule decides on (domain.QuestionUse)
// for the live bank question questionID edited in context: its owner, whether
// it is a group member, the media it binds, and the live drafts and live sets,
// other than the context, that use it. A draft uses it through
// test_section_questions or test_section_units, whether the test is active or
// archived; a deleted test or set no longer counts. Callers run it after
// LockForDraftUse has locked the question in the same transaction, so no
// draft or set can start using it, nor the question be deleted, before they
// commit (an add takes the same lock, and so does a delete).
func ContextUse(ctx context.Context, q db.Querier, questionID string, within domain.EditContext) (domain.QuestionUse, error) {
	var testID, setID *string
	switch within.Kind {
	case domain.ContextTest:
		testID = &within.ID
	case domain.ContextSet:
		setID = &within.ID
	}
	var use domain.QuestionUse
	var attachment *string
	var prompt, explanation json.RawMessage
	err := q.QueryRow(ctx, `
		SELECT q.id::text, q.owner_id::text, q.context_group_id IS NOT NULL, q.media_asset_id::text,
		       q.prompt_content, q.explanation_content,
		       (SELECT count(DISTINCT s.test_id) FROM app.test_sections s
		          JOIN app.tests t ON t.id = s.test_id AND t.deleted_at IS NULL
		         WHERE s.test_id IS DISTINCT FROM $2::uuid
		           AND (EXISTS (SELECT 1 FROM app.test_section_questions sq
		                         WHERE sq.test_section_id = s.id AND sq.question_id = q.id)
		             OR EXISTS (SELECT 1 FROM app.test_section_units u
		                         WHERE u.test_section_id = s.id AND u.question_id = q.id))),
		       (SELECT count(*) FROM app.question_set_items i
		          JOIN app.question_sets st ON st.id = i.set_id AND st.deleted_at IS NULL
		         WHERE i.question_id = q.id AND i.set_id IS DISTINCT FROM $3::uuid)
		  FROM app.questions q
		 WHERE q.id = $1::uuid AND q.deleted_at IS NULL`, questionID, testID, setID).
		Scan(&use.ID, &use.OwnerID, &use.GroupMember, &attachment, &prompt, &explanation, &use.OtherDrafts, &use.OtherSets)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.QuestionUse{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.QuestionUse{}, fmt.Errorf("questions: context use: %w", err)
	}
	if attachment != nil {
		use.Media = append(use.Media, strings.ToLower(*attachment))
	}
	shown, err := content.QuestionMedia(prompt, explanation)
	if err != nil {
		return domain.QuestionUse{}, fmt.Errorf("questions: context use media: %w", err)
	}
	for _, ref := range shown {
		use.Media = append(use.Media, strings.ToLower(ref.ID))
	}
	slices.Sort(use.Media)
	use.Media = slices.Compact(use.Media)
	return use, nil
}

// WriteInContext writes in, an edit of the bank question use.ID made in
// context, as the copy-on-write rule decides (domain.QuestionUse.Forks): in
// place, or as a new question owned by the question's owner and created by the
// editor, which the caller then points its context at; forked says which. It
// runs inside the caller's transaction, which holds the context's row lock and
// the question's (LockForDraftUse), so the rule is applied to what ContextUse
// read under them. The edit's media are resolved and checked here: the media
// the question already binds bind whoever edits it, any other asset under the
// editor's own scope, refused as a missing one when it cannot be read.
// Either way the edit is checked against the question's revision
// (ExpectedUpdatedAt) and keeps what it omits as updateQuestion does: the
// stored prose, an option's formatting and the audio's allowPause.
func (s *Postgres) WriteInContext(ctx context.Context, in domain.WriteInput, use domain.QuestionUse, within domain.EditContext) (domain.Question, bool, error) {
	tx, err := s.Begin(ctx)
	if err != nil {
		return domain.Question{}, false, fmt.Errorf("questions: begin context write: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	in.ID, in.KeptMedia, in.InContext, in.SourceMedia = use.ID, use.Media, true, false
	if in.MediaAssetKind, err = s.attachmentKind(ctx, tx, in, use); err != nil {
		return domain.Question{}, false, err
	}
	if err := in.Input.Validate(in.MediaAssetKind); err != nil {
		return domain.Question{}, false, err
	}
	inner := &Postgres{Repository: db.NewRepository(db.NewContext(tx)), media: s.media}
	forked := use.Forks(in.ActorID, within)
	var written domain.Question
	if forked {
		written, err = inner.fork(ctx, tx, in, use, within)
	} else {
		written, err = inner.write(ctx, in, true, nil)
	}
	if err != nil {
		return domain.Question{}, false, err
	}
	written.Access = domain.AccessEdit
	if in.All || in.ActorID == use.OwnerID {
		written.Access = domain.AccessOwner
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Question{}, false, fmt.Errorf("questions: commit context write: %w", err)
	}
	return written, forked, nil
}

// attachmentKind is the kind of the asset the edit attaches (mediaAssetId),
// read under the scope it binds in: everyone's live assets for one the
// question already binds or for scope.all, the editor's own otherwise. An
// asset that scope cannot read is domain.ErrMediaNotFound, as a missing one is.
func (s *Postgres) attachmentKind(ctx context.Context, tx pgx.Tx, in domain.WriteInput, use domain.QuestionUse) (*string, error) {
	if in.Input.MediaAssetID == nil {
		return nil, nil
	}
	if s.media == nil {
		return nil, domain.ErrMediaNotFound
	}
	id := *in.Input.MediaAssetID
	scope := access.Scope{UserID: in.ActorID, All: in.All}
	if use.Keeps(id) {
		scope = groupAuthorized
	}
	kinds, err := s.media.ReadableKinds(ctx, tx, scope, []string{id})
	if err != nil {
		return nil, err
	}
	kind, ok := kinds[strings.ToLower(id)]
	if !ok {
		return nil, domain.ErrMediaNotFound
	}
	return &kind, nil
}

// fork makes the edit a new question: R6's duplicate path with the edit
// applied. The edit is first held to the original as an update would be,
// under its row lock: its revision, and what the edit omits kept from the
// original (its prose, an option's formatting, the audio's allowPause). The
// copy then belongs to the original's owner, is created and last written by
// the editor, takes fresh gap ids as a duplicate does, and is audited as
// created and as forked from the original in context.
func (s *Postgres) fork(ctx context.Context, tx pgx.Tx, in domain.WriteInput, use domain.QuestionUse, within domain.EditContext) (domain.Question, error) {
	if err := prepareQuestionContent(ctx, tx, &in, true, nil); err != nil {
		return domain.Question{}, err
	}
	if err := keepOmitted(ctx, tx, &in.Input, use.ID); err != nil {
		return domain.Question{}, err
	}
	in.ID, in.OwnerID, in.ExpectedUpdatedAt = "", use.OwnerID, nil
	if err := in.Input.RebindGaps(uuid.NewString); err != nil {
		return domain.Question{}, err
	}
	written, err := s.write(ctx, in, false, nil)
	if err != nil {
		return domain.Question{}, err
	}
	diff, err := json.Marshal(map[string]any{
		"from": use.ID, "to": written.ID,
		"context": map[string]string{"kind": string(within.Kind), "id": within.ID},
	})
	if err != nil {
		return domain.Question{}, err
	}
	if err := audit.Write(ctx, tx, audit.Entry{
		ActorUserID: &in.ActorID,
		Action:      "question.forked",
		Entity:      entityQuestion,
		EntityID:    &written.ID,
		OccurredAt:  in.Now,
		IP:          opt.String(in.IP),
		UserAgent:   opt.String(in.UserAgent),
		Diff:        diff,
	}); err != nil {
		return domain.Question{}, err
	}
	return written, nil
}

// keepOmitted fills in, a fork's edit of the question originalID, with what
// an update of the original would keep where the edit leaves it out, read
// under the original's row lock: an option sent without content keeps the
// formatting of the option it updates, or is refused as options[i].content as
// an update refuses it (preserveOptionContent), and an audio policy without
// allowPause keeps the stored one. The create path the copy is written by
// would otherwise drop the formatting and store allowPause as true. The
// options and audio policy are copied first, so the caller's input is left as
// it was.
func keepOmitted(ctx context.Context, tx pgx.Tx, in *domain.Input, originalID string) error {
	if in.Type != domain.TFNG && in.Type.HasOptions() {
		in.Options = slices.Clone(in.Options)
		if err := preserveOptionContent(ctx, tx, originalID, in.Options); err != nil {
			return err
		}
	}
	if in.Audio == nil || in.Audio.AllowPause != nil {
		return nil
	}
	var stored *bool
	if err := tx.QueryRow(ctx, `SELECT audio_allow_pause FROM app.questions WHERE id = $1::uuid`, originalID).Scan(&stored); err != nil {
		return fmt.Errorf("questions: fork audio policy: %w", err)
	}
	audio := *in.Audio
	audio.AllowPause = stored
	in.Audio = &audio
	return nil
}
