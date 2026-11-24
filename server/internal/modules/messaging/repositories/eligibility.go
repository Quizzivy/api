// Package repositories keeps the messaging context's conversations, messages
// and read pointers (T-R7.9's tables), and reads the classes and accounts
// that decide who reads and who posts, and the ties (app.messaging_ties) that
// tell a former student or teacher. Nothing here writes an audit row:
// the append-only messages table is the record (D17).
package repositories

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"quizzivy/internal/modules/messaging/domain"
	"quizzivy/internal/platform/db"
	"quizzivy/internal/shared/access"
	"quizzivy/internal/shared/opt"
	"quizzivy/internal/shared/retention"
	"quizzivy/internal/shared/visibility"
)

// DefaultLimit is a thread page's size when the request names none, as the
// contract's default.
const DefaultLimit = domain.DefaultPage

// Postgres keeps conversations through dbx.
type Postgres struct {
	db.Repository
}

// NewPostgres keeps conversations through dbx.
func NewPostgres(dbx db.Context) *Postgres {
	return &Postgres{Repository: db.NewRepository(dbx)}
}

var _ domain.Repository = (*Postgres)(nil)

// The facts each have one definition, shared with visibility.MessagingPeerIDs
// and visibility.CanMessage: a direct conversation, the account being
// enabled, a student and not anonymised.
const directFactsSQL = `
	SELECT u.disabled_at IS NULL, %s, NOT (%s),
	       EXISTS (SELECT 1 FROM app.conversations v WHERE v.kind = 'direct' AND v.staff_id = $1::uuid AND v.student_id = u.id)
	  FROM app.users u
	 WHERE u.id = $2::uuid`

// DirectFacts reads what decides staffID's direct thread with studentID.
func (p *Postgres) DirectFacts(ctx context.Context, staffID, studentID string) (domain.DirectFacts, error) {
	return directFacts(ctx, p, staffID, studentID)
}

func directFacts(ctx context.Context, q db.Querier, staffID, studentID string) (domain.DirectFacts, error) {
	f := domain.DirectFacts{Exists: true, Self: staffID == studentID}
	err := q.QueryRow(ctx, fmt.Sprintf(directFactsSQL, visibility.StudentLike("u"), retention.NotAnonymousOf("u")), opt.String(staffID), studentID).
		Scan(&f.Enabled, &f.Student, &f.Anonymised, &f.Conversation)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.DirectFacts{Self: f.Self}, nil
	}
	if err != nil {
		return domain.DirectFacts{}, fmt.Errorf("messaging: direct facts: %w", err)
	}
	return f, nil
}

// activeStudents counts the enabled students of the class the SQL expression
// class names: who an announcement reaches, and whom "Seen by" counts.
func activeStudents(class string) string {
	return `(SELECT count(*)::int FROM app.class_members cm
	   JOIN app.users st ON st.id = cm.user_id AND st.disabled_at IS NULL AND ` + visibility.StudentLike("st") + `
	  WHERE cm.class_id = ` + class + `)`
}

const channelSQL = `
	SELECT c.id::text, c.name, c.archived_at IS NOT NULL, %s
	  FROM app.classes c
	 WHERE c.id = $1::uuid AND ($2::boolean OR c.teacher_id = $3::uuid)`

// Channel reads the class classID as scope reaches it: its teacher, or
// scope.all, archived or not; ErrNotFound for anyone else.
func (p *Postgres) Channel(ctx context.Context, scope access.Scope, classID string) (domain.Channel, error) {
	return channel(ctx, p, scope, classID, "")
}

func channel(ctx context.Context, q db.Querier, scope access.Scope, classID, lock string) (domain.Channel, error) {
	var c domain.Channel
	err := q.QueryRow(ctx, fmt.Sprintf(channelSQL, activeStudents("c.id"))+lock, classID, scope.All, opt.String(scope.UserID)).
		Scan(&c.ClassID, &c.Name, &c.Archived, &c.StudentCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Channel{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Channel{}, fmt.Errorf("messaging: channel: %w", err)
	}
	return c, nil
}

func directConversation(ctx context.Context, q db.Querier, staffID, studentID string) (string, error) {
	return conversationID(ctx, q, `SELECT v.id::text FROM app.conversations v
		 WHERE v.kind = 'direct' AND v.staff_id = $1::uuid AND v.student_id = $2::uuid`, staffID, studentID)
}

func classConversation(ctx context.Context, q db.Querier, classID string) (string, error) {
	return conversationID(ctx, q, `SELECT v.id::text FROM app.conversations v WHERE v.kind = 'class' AND v.class_id = $1::uuid`, classID)
}

func conversationID(ctx context.Context, q db.Querier, sql string, args ...any) (string, error) {
	var id string
	err := q.QueryRow(ctx, sql, args...).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("messaging: conversation: %w", err)
	}
	return id, nil
}
