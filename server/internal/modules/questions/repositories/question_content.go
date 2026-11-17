package repositories

import (
	"context"
	"encoding/json"
	"errors"
	"quizzivy/internal/modules/questions/domain"
	"quizzivy/internal/shared/content"
	"quizzivy/internal/shared/opt"
	"quizzivy/internal/shared/provenance"
	"quizzivy/internal/shared/validation"
	"time"

	"github.com/jackc/pgx/v5"
)

func prepareQuestionContent(ctx context.Context, tx pgx.Tx, write *domain.WriteInput, update bool, groupID *string) error {
	in := &write.Input
	if !update {
		return validateStoredContent(write)
	}
	var prompt string
	var explanation *string
	var promptContent, explanationContent json.RawMessage
	var last provenance.Row
	err := tx.QueryRow(ctx, `SELECT q.prompt, q.explanation, q.prompt_content, q.explanation_content, `+provenance.Columns("q")+`
        FROM app.questions q WHERE q.id = $1 AND q.deleted_at IS NULL
          AND q.context_group_id IS NOT DISTINCT FROM $2::uuid AND ($3::boolean OR q.owner_id = $4::uuid) FOR UPDATE OF q`,
		write.ID, groupID, write.All || write.InContext || groupID != nil, opt.String(write.ActorID)).
		Scan(append([]any{&prompt, &explanation, &promptContent, &explanationContent}, last.Dest()...)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	if err != nil {
		return err
	}
	if err := fresh(last.LastWrite(), write.ExpectedUpdatedAt); err != nil {
		return err
	}
	if err := preserveProse("promptContent", content.ParsePromptWithMedia, &in.PromptContent, promptContent, &in.Prompt, &prompt); err != nil {
		return err
	}
	if err := preserveProse("explanationContent", content.ParseExplanationWithMedia, &in.ExplanationContent, explanationContent, in.Explanation, explanation); err != nil {
		return err
	}
	return validateStoredContent(write)
}

// fresh is nil when the update names no revision, as a v0.13 client's does,
// or names the one it locked, and otherwise domain.ErrStaleWrite naming the
// question's last write, which the editor had not seen.
func fresh(last provenance.LastWrite, expected *time.Time) error {
	if expected == nil || last.At.Truncate(time.Microsecond).Equal(expected.Truncate(time.Microsecond)) {
		return nil
	}
	return provenance.Stale(domain.ErrStaleWrite, last)
}

// validateStoredContent checks the prose the write will store, an omitted
// document included, against the media rules and the question's audio: a
// preserved prompt that places the audio needs the audio this write keeps.
func validateStoredContent(write *domain.WriteInput) error {
	if err := write.Input.ValidateContent(); err != nil {
		return err
	}
	return write.Input.ValidatePlacedAudio(write.MediaAssetKind)
}

func preserveProse(field string, parse func([]byte) (content.Document, error), next *json.RawMessage, old json.RawMessage, text, previous *string) error {
	if *next != nil || old == nil {
		return nil
	}
	if text == nil || previous == nil || content.NFC(*text) != content.NFC(*previous) {
		return validation.Refuse(field, "Câu hỏi đã có định dạng. Hãy tải lại trước khi sửa nội dung.",
			"The question has formatting. Reload it before you change its text.")
	}
	*next, *text = composedProse(parse, old, *text, *previous)
	return nil
}

// composedProse is the stored document an update that omitted it keeps, in
// NFC, parsed under the field's media rules so a stored image or audio marker
// survives the composition (DG-109). A document those rules refuse is kept as
// stored, and the content check refuses the write.
func composedProse(parse func([]byte) (content.Document, error), old json.RawMessage, text, previous string) (json.RawMessage, string) {
	document, err := parse(old)
	if err != nil {
		return old, previous
	}
	composed, err := content.Normalize(document)
	if err != nil || content.NFC(composed.PlainText()) != content.NFC(text) {
		return old, previous
	}
	encoded, err := composed.MarshalJSON()
	if err != nil {
		return old, previous
	}
	return encoded, composed.PlainText()
}
