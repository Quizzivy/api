package repositories

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"

	"quizzivy/internal/modules/messaging/domain"
	"quizzivy/internal/platform/db"
	"quizzivy/internal/shared/access"
	"quizzivy/internal/shared/opt"
	"quizzivy/internal/shared/visibility"
)

// messageColumns reads a message m of conversation v, its sender s and its
// file a; a student is the sender who is the conversation's student.
var messageColumns = `
	SELECT m.id::text, m.conversation_id::text, m.sender_id::text, ` + nameOf("s") + `, s.avatar_key,
	       CASE WHEN m.sender_id = v.student_id THEN 'student' ELSE 'staff' END,
	       m.body, a.id::text, a.original_filename, a.bytes, a.mime_type, m.created_at`

const messageFrom = `
	  FROM app.messages m
	  JOIN app.conversations v ON v.id = m.conversation_id
	  JOIN app.users s ON s.id = m.sender_id
	  LEFT JOIN app.message_attachments a ON a.id = m.attachment_id`

// seenColumn counts the class's active students whose read pointer in the
// channel has reached message m, for the class in placeholder $n.
func seenColumn(n int) string {
	return fmt.Sprintf(`(SELECT count(*)::int FROM app.class_members cm
	   JOIN app.users st ON st.id = cm.user_id AND st.disabled_at IS NULL AND %s
	   JOIN app.conversation_reads r ON r.conversation_id = m.conversation_id AND r.user_id = cm.user_id AND r.last_read_message_id >= m.id
	  WHERE cm.class_id = $%d::uuid)`, visibility.StudentLike("st"), n)
}

func scanMessage(row pgx.Row, extra ...any) (domain.Message, error) {
	var m domain.Message
	var side string
	var attachmentID, filename, mimeType *string
	var bytes *int
	dest := append([]any{&m.ID, &m.ConversationID, &m.SenderID, &m.SenderName, &m.SenderAvatarKey, &side,
		&m.Body, &attachmentID, &filename, &bytes, &mimeType, &m.CreatedAt}, extra...)
	if err := row.Scan(dest...); err != nil {
		return domain.Message{}, err
	}
	m.Side = domain.Side(side)
	if attachmentID != nil {
		m.Attachment = &domain.Attachment{ID: *attachmentID, Filename: deref(filename), Bytes: derefInt(bytes), MimeType: deref(mimeType)}
	}
	return m, nil
}

func derefInt(n *int) int {
	if n == nil {
		return 0
	}
	return *n
}

// DirectPage reads one page of staffID's thread with studentID, empty when
// none exists yet.
func (p *Postgres) DirectPage(ctx context.Context, staffID, studentID string, cursor domain.Cursor) (domain.Page, error) {
	conversation, err := directConversation(ctx, p, staffID, studentID)
	if err != nil {
		return domain.Page{}, err
	}
	return p.page(ctx, conversation, "", cursor)
}

// ChannelPage reads one page of the channel of classID, each post with its
// SeenBy: of the class's active students now, those whose pointer has reached
// it.
func (p *Postgres) ChannelPage(ctx context.Context, classID string, cursor domain.Cursor) (domain.Page, error) {
	conversation, err := classConversation(ctx, p, classID)
	if err != nil {
		return domain.Page{}, err
	}
	return p.page(ctx, conversation, classID, cursor)
}

// page reads one page of conversation, the newest cursor.Limit messages or
// those older than Before (newest first, then turned oldest first), or the
// oldest cursor.Limit newer than After. A cursor id compares in uuidv7 order,
// so it need not be a message of the thread. classID, when set, adds each
// post's SeenBy.
func (p *Postgres) page(ctx context.Context, conversation, classID string, cursor domain.Cursor) (domain.Page, error) {
	out := domain.Page{Messages: []domain.Message{}}
	if conversation == "" {
		return out, nil
	}
	order := "DESC"
	if cursor.After != "" {
		order = "ASC"
	}
	args := []any{conversation, opt.String(cursor.Before), opt.String(cursor.After), cursor.Limit + 1}
	columns := messageColumns
	var of int
	if classID != "" {
		args = append(args, classID)
		columns += `, ` + seenColumn(len(args))
		if err := p.QueryRow(ctx, `SELECT `+activeStudents("$1::uuid"), classID).Scan(&of); err != nil {
			return domain.Page{}, fmt.Errorf("messaging: channel students: %w", err)
		}
	}
	messages, err := db.QueryMany(ctx, p, columns+messageFrom+`
		 WHERE m.conversation_id = $1::uuid
		   AND ($2::uuid IS NULL OR m.id < $2::uuid)
		   AND ($3::uuid IS NULL OR m.id > $3::uuid)
		 ORDER BY m.id `+order+`
		 LIMIT $4`, args, func(rows pgx.Rows) (domain.Message, error) {
		if classID == "" {
			return scanMessage(rows)
		}
		var seen int
		m, err := scanMessage(rows, &seen)
		m.SeenBy = &domain.SeenBy{Seen: seen, Of: of}
		return m, err
	})
	if err != nil {
		return domain.Page{}, fmt.Errorf("messaging: page: %w", err)
	}
	more := len(messages) > cursor.Limit
	if more {
		messages = messages[:cursor.Limit]
	}
	if cursor.After == "" {
		slices.Reverse(messages)
		if more {
			out.NextBefore = &messages[0].ID
		}
	}
	if messages != nil {
		out.Messages = messages
	}
	var latest string
	err = p.QueryRow(ctx, `SELECT m.id::text FROM app.messages m WHERE m.conversation_id = $1::uuid ORDER BY m.id DESC LIMIT 1`, conversation).Scan(&latest)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
	case err != nil:
		return domain.Page{}, fmt.Errorf("messaging: latest: %w", err)
	default:
		out.LatestID = &latest
	}
	return out, nil
}

// target is the thread a post goes to, as its transaction resolved it: find
// reads its conversation's id, empty before the first post; allow is the rule
// a new message must pass; open makes the conversation.
type target struct {
	find  func(ctx context.Context, tx pgx.Tx) (string, error)
	allow func() error
	open  func(ctx context.Context, tx pgx.Tx) error
}

// post writes one post in one transaction. resolve reads the facts and
// answers a thread the sender cannot read; then a repeated clientId returns
// the message it made, the post's rule is asked, the file checked (one the
// sender uploaded and nobody has sent), the conversation made by the first
// post and locked, and the message written with its file.
func (p *Postgres) post(ctx context.Context, name string, post domain.Post, resolve func(context.Context, pgx.Tx) (target, error)) (domain.Posted, error) {
	var out domain.Posted
	err := p.InTx(ctx, name, func(tx pgx.Tx) error {
		t, err := resolve(ctx, tx)
		if err != nil {
			return err
		}
		find := func() (string, error) { return t.find(ctx, tx) }
		if replay, done, err := repeated(ctx, tx, post, find); done || err != nil {
			out = replay
			return err
		}
		if err := t.allow(); err != nil {
			return err
		}
		if err := attachable(ctx, tx, post); err != nil {
			return err
		}
		if err := t.open(ctx, tx); err != nil {
			return err
		}
		thread, err := find()
		if err != nil {
			return err
		}
		if err := serialise(ctx, tx, thread); err != nil {
			return err
		}
		out, err = write(ctx, tx, thread, post)
		return err
	})
	return out, err
}

// serialise holds thread's row until the post commits, before the message's
// id is made, so a thread's posts take their uuidv7 ids in the order they
// commit. Without it a post could make its id, and a later one make a greater
// id and commit first: a reader whose poll or read pointer reached the later
// one would never get the earlier, and would count it as read (page, markRead
// and seenBy compare ids). FOR NO KEY UPDATE is the lock the last_message_at
// UPDATE takes anyway; it leaves the FOR KEY SHARE of a read pointer's foreign
// key alone, so a reader marking the thread read does not wait.
func serialise(ctx context.Context, tx pgx.Tx, thread string) error {
	if _, err := tx.Exec(ctx, `SELECT 1 FROM app.conversations WHERE id = $1::uuid FOR NO KEY UPDATE`, thread); err != nil {
		return fmt.Errorf("messaging: lock thread: %w", err)
	}
	return nil
}

// PostDirect writes post to staffID's thread with studentID in one
// transaction (domain.Repository).
func (p *Postgres) PostDirect(ctx context.Context, staffID, studentID string, post domain.Post) (domain.Posted, error) {
	rules := domain.Eligibility{}
	return p.post(ctx, "messaging: post direct", post, func(ctx context.Context, tx pgx.Tx) (target, error) {
		return pairTarget(ctx, tx, rules.ReadDirect, rules.PostDirect,
			func(ctx context.Context, q db.Querier) (domain.DirectFacts, error) {
				return directFacts(ctx, q, staffID, studentID)
			},
			func(ctx context.Context, tx pgx.Tx) (string, error) {
				return directConversation(ctx, tx, staffID, studentID)
			},
			`INSERT INTO app.conversations (kind, staff_id, student_id, last_message_at) VALUES ('direct', $1::uuid, $2::uuid, now())
			 ON CONFLICT (staff_id, student_id) WHERE kind = 'direct' DO NOTHING`, staffID, studentID)
	})
}

// pairTarget is the target of a post to a two-person thread: the facts its
// transaction reads, judged by read (a thread the sender cannot read answers
// there) and then by allow for the new message; find reads the thread, and
// the first post makes it with open, an INSERT … ON CONFLICT DO NOTHING
// over args.
func pairTarget[F any](ctx context.Context, tx pgx.Tx, read, allow func(F) error,
	facts func(context.Context, db.Querier) (F, error), find func(context.Context, pgx.Tx) (string, error),
	open string, args ...any) (target, error) {
	f, err := facts(ctx, tx)
	if err != nil {
		return target{}, err
	}
	if err := read(f); err != nil {
		return target{}, err
	}
	return target{
		find:  find,
		allow: func() error { return allow(f) },
		open: func(ctx context.Context, tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, open, args...); err != nil {
				return fmt.Errorf("messaging: open thread: %w", err)
			}
			return nil
		},
	}, nil
}

// PostChannel writes post to the channel of classID, which it holds FOR
// SHARE, in one transaction (domain.Repository). The post carries its SeenBy.
func (p *Postgres) PostChannel(ctx context.Context, scope access.Scope, classID string, mayWrite bool, post domain.Post) (domain.Posted, error) {
	out, err := p.post(ctx, "messaging: post announcement", post, func(ctx context.Context, tx pgx.Tx) (target, error) {
		class, err := channel(ctx, tx, scope, classID, ` FOR SHARE OF c`)
		if err != nil && !errors.Is(err, domain.ErrNotFound) {
			return target{}, err
		}
		facts := domain.ChannelFacts{InScope: err == nil, Archived: class.Archived, MayWrite: mayWrite}
		rules := domain.Eligibility{}
		if err := rules.ReadChannel(facts); err != nil {
			return target{}, err
		}
		return target{
			find: func(ctx context.Context, tx pgx.Tx) (string, error) {
				return classConversation(ctx, tx, classID)
			},
			allow: func() error { return rules.PostChannel(facts) },
			open: func(ctx context.Context, tx pgx.Tx) error {
				if _, err := tx.Exec(ctx, `
					INSERT INTO app.conversations (kind, class_id, last_message_at) VALUES ('class', $1::uuid, now())
					ON CONFLICT (class_id) WHERE kind = 'class' DO NOTHING`, classID); err != nil {
					return fmt.Errorf("messaging: open channel: %w", err)
				}
				return nil
			},
		}, nil
	})
	if err == nil {
		out.Message.SeenBy, err = p.seenBy(ctx, classID, out.Message.ID)
	}
	return out, err
}

// seenBy is a post's SeenBy in the channel of classID.
func (p *Postgres) seenBy(ctx context.Context, classID, messageID string) (*domain.SeenBy, error) {
	var s domain.SeenBy
	if err := p.QueryRow(ctx, `SELECT `+activeStudents("$1::uuid")+`, `+seenColumn(1)+` FROM app.messages m WHERE m.id = $2::uuid`,
		classID, messageID).Scan(&s.Of, &s.Seen); err != nil {
		return nil, fmt.Errorf("messaging: seen by: %w", err)
	}
	return &s, nil
}

// repeated finds the message the sender's clientId already made: done with it
// when it is in the thread find reads, ErrClientIDTaken when it is another
// thread's, and not done when there is none. find runs after the message is
// read, so a first post with the same clientId that committed meanwhile,
// making the thread, is this one's replay rather than another thread's.
func repeated(ctx context.Context, tx pgx.Tx, post domain.Post, find func() (string, error)) (domain.Posted, bool, error) {
	m, err := scanMessage(tx.QueryRow(ctx, messageColumns+messageFrom+`
		 WHERE m.sender_id = $1::uuid AND m.client_id = $2::uuid`, post.SenderID, post.ClientID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Posted{}, false, nil
	}
	if err != nil {
		return domain.Posted{}, true, fmt.Errorf("messaging: repeated post: %w", err)
	}
	thread, err := find()
	if err != nil {
		return domain.Posted{}, true, err
	}
	if m.ConversationID != thread {
		return domain.Posted{}, true, domain.ErrClientIDTaken
	}
	return domain.Posted{Message: m}, true, nil
}

// write inserts post into thread with its file and moves the thread's
// last_message_at. A concurrent call with the same clientId that committed
// first makes this one its replay; a concurrent post that took the file
// first, or the sweep that removed it, makes the file a missing one.
func write(ctx context.Context, tx pgx.Tx, thread string, post domain.Post) (domain.Posted, error) {
	var id string
	var at time.Time
	err := tx.QueryRow(ctx, `
		INSERT INTO app.messages (conversation_id, sender_id, client_id, body, attachment_id) VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5::uuid)
		ON CONFLICT (sender_id, client_id) DO NOTHING
		RETURNING id::text, created_at`, thread, post.SenderID, post.ClientID, post.Content.Body, attachmentOf(post)).Scan(&id, &at)
	if errors.Is(err, pgx.ErrNoRows) {
		replay, _, err := repeated(ctx, tx, post, func() (string, error) { return thread, nil })
		return replay, err
	}
	if taken(err) {
		return domain.Posted{}, domain.ErrAttachmentNotFound
	}
	if err != nil {
		return domain.Posted{}, fmt.Errorf("messaging: write message: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE app.conversations SET last_message_at = greatest(last_message_at, $2) WHERE id = $1::uuid`, thread, at); err != nil {
		return domain.Posted{}, fmt.Errorf("messaging: move last_message_at: %w", err)
	}
	m, err := scanMessage(tx.QueryRow(ctx, messageColumns+messageFrom+` WHERE m.id = $1::uuid`, id))
	if err != nil {
		return domain.Posted{}, fmt.Errorf("messaging: read back message: %w", err)
	}
	return domain.Posted{Message: m, Created: true}, nil
}
