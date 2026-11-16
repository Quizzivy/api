package repositories

import (
	"context"
	"fmt"

	"quizzivy/internal/modules/messaging/domain"
	"quizzivy/internal/platform/db"
)

// MarkDirectRead moves the reader's pointer in staffID's thread with
// studentID forward to the mark's message: ErrNotInThread when the message is
// not one of that thread's, and nothing when the pointer is already past it.
func (p *Postgres) MarkDirectRead(ctx context.Context, staffID, studentID string, mark domain.ReadMark) error {
	return p.markRead(ctx, `v.kind = 'direct' AND v.staff_id = $3::uuid AND v.student_id = $4::uuid`, mark, staffID, studentID)
}

// MarkChannelRead is MarkDirectRead for the channel of classID.
func (p *Postgres) MarkChannelRead(ctx context.Context, classID string, mark domain.ReadMark) error {
	return p.markRead(ctx, `v.kind = 'class' AND v.class_id = $3::uuid`, mark, classID)
}

// markRead upserts the pointer, which compares in uuidv7 order and only moves
// forward, for the message mark names in the conversation thread selects.
func (p *Postgres) markRead(ctx context.Context, thread string, mark domain.ReadMark, args ...any) error {
	target := `SELECT m.conversation_id, m.id FROM app.messages m JOIN app.conversations v ON v.id = m.conversation_id
		 WHERE m.id = $1::uuid AND ` + thread
	params := append([]any{mark.MessageID, mark.ReaderID}, args...)
	found, err := db.Exists(ctx, p, target+` AND $2::uuid IS NOT NULL`, params...)
	if err != nil {
		return fmt.Errorf("messaging: read pointer target: %w", err)
	}
	if !found {
		return domain.ErrNotInThread
	}
	if _, err := p.Exec(ctx, `
		INSERT INTO app.conversation_reads (conversation_id, user_id, last_read_message_id)
		SELECT t.conversation_id, $2::uuid, t.id FROM (`+target+`) t
		ON CONFLICT (conversation_id, user_id) DO UPDATE
		   SET last_read_message_id = EXCLUDED.last_read_message_id, read_at = now()
		 WHERE app.conversation_reads.last_read_message_id < EXCLUDED.last_read_message_id`, params...); err != nil {
		return fmt.Errorf("messaging: move read pointer: %w", err)
	}
	return nil
}
