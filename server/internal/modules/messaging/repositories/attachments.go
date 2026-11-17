package repositories

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"quizzivy/internal/modules/messaging/domain"
	"quizzivy/internal/platform/db"
	"quizzivy/internal/shared/opt"
)

// InsertAttachment records a file the upload stored, unsent
// (domain.Repository).
func (p *Postgres) InsertAttachment(ctx context.Context, a domain.NewAttachment) (domain.Attachment, error) {
	out := domain.Attachment{ID: a.ID}
	err := p.QueryRow(ctx, `
		INSERT INTO app.message_attachments (id, uploaded_by, storage_key, mime_type, bytes, original_filename, checksum_sha256)
		VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6, $7)
		RETURNING original_filename, bytes, mime_type`,
		a.ID, a.UploadedBy, a.StorageKey, a.MimeType, a.Bytes, a.Filename, a.Checksum).
		Scan(&out.Filename, &out.Bytes, &out.MimeType)
	if err != nil {
		return domain.Attachment{}, fmt.Errorf("messaging: insert attachment: %w", err)
	}
	return out, nil
}

// StoredAttachment reads file id and the thread of the message that holds
// it, or domain.ErrAttachmentNotFound (domain.Repository).
func (p *Postgres) StoredAttachment(ctx context.Context, id string) (domain.StoredAttachment, error) {
	var a domain.StoredAttachment
	var kind, staff, student, class *string
	err := p.QueryRow(ctx, `
		SELECT a.id::text, a.original_filename, a.bytes, a.mime_type, a.storage_key, a.uploaded_by::text,
		       v.kind, v.staff_id::text, v.student_id::text, v.class_id::text
		  FROM app.message_attachments a
		  LEFT JOIN app.messages m ON m.attachment_id = a.id
		  LEFT JOIN app.conversations v ON v.id = m.conversation_id
		 WHERE a.id = $1::uuid`, id).
		Scan(&a.ID, &a.Filename, &a.Bytes, &a.MimeType, &a.StorageKey, &a.UploadedBy, &kind, &staff, &student, &class)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.StoredAttachment{}, domain.ErrAttachmentNotFound
	}
	if err != nil {
		return domain.StoredAttachment{}, fmt.Errorf("messaging: read attachment: %w", err)
	}
	if kind != nil {
		a.Thread = &domain.AttachmentThread{Kind: domain.Kind(*kind), StaffID: deref(staff), StudentID: deref(student), ClassID: deref(class)}
	}
	return a, nil
}

// DeleteUnsentAttachments deletes at most limit files no message holds that
// were uploaded before before, oldest first, and returns their keys
// (domain.Repository). A post that names one of them meanwhile either commits
// first, and then the RESTRICT key from app.messages fails this statement, so
// the step stops and the next run skips the file, or waits on the deleted row
// and answers the file as missing.
func (p *Postgres) DeleteUnsentAttachments(ctx context.Context, before time.Time, limit int) ([]string, error) {
	keys, err := db.QueryMany(ctx, p, `
		DELETE FROM app.message_attachments a
		 WHERE a.id IN (SELECT x.id FROM app.message_attachments x
		                 WHERE x.created_at < $1
		                   AND NOT EXISTS (SELECT 1 FROM app.messages m WHERE m.attachment_id = x.id)
		                 ORDER BY x.created_at, x.id
		                 LIMIT $2)
		RETURNING a.storage_key`, []any{before, limit}, func(rows pgx.Rows) (string, error) {
		var key string
		return key, rows.Scan(&key)
	})
	if err != nil {
		return nil, fmt.Errorf("messaging: delete unsent attachments: %w", err)
	}
	return keys, nil
}

// NamedKeys is the subset of keys some file's row stores its object under
// (domain.Repository).
func (p *Postgres) NamedKeys(ctx context.Context, keys []string) (map[string]bool, error) {
	out := map[string]bool{}
	if len(keys) == 0 {
		return out, nil
	}
	named, err := db.QueryMany(ctx, p, `SELECT storage_key FROM app.message_attachments WHERE storage_key = ANY($1::text[])`,
		[]any{keys}, func(rows pgx.Rows) (string, error) {
			var key string
			return key, rows.Scan(&key)
		})
	if err != nil {
		return nil, fmt.Errorf("messaging: named attachment keys: %w", err)
	}
	for _, key := range named {
		out[key] = true
	}
	return out, nil
}

// attachable lets a post name only a file its sender uploaded that no message
// holds yet: anything else, another sender's, a sent one or none, answers
// domain.ErrAttachmentNotFound. The check reads; the message's UNIQUE and
// foreign key on attachment_id decide a race (taken), as the app role may not
// lock a file's row (00163 revokes its UPDATE).
func attachable(ctx context.Context, tx pgx.Tx, post domain.Post) error {
	if post.Content.AttachmentID == "" {
		return nil
	}
	var mine bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM app.message_attachments a
		                WHERE a.id = $1::uuid AND a.uploaded_by = $2::uuid
		                  AND NOT EXISTS (SELECT 1 FROM app.messages m WHERE m.attachment_id = a.id))`,
		post.Content.AttachmentID, post.SenderID).Scan(&mine); err != nil {
		return fmt.Errorf("messaging: attachable: %w", err)
	}
	if !mine {
		return domain.ErrAttachmentNotFound
	}
	return nil
}

// taken answers a message INSERT that lost its file to a concurrent post (the
// UNIQUE on attachment_id) or to the sweep (its foreign key) as a missing
// file.
func taken(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && (pg.ConstraintName == "messages_attachment_id_key" || pg.ConstraintName == "messages_attachment_id_fkey")
}

func attachmentOf(post domain.Post) *string { return opt.String(post.Content.AttachmentID) }
