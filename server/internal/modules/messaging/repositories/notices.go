package repositories

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"quizzivy/internal/modules/messaging/domain"
	"quizzivy/internal/shared/access"
	"quizzivy/internal/shared/opt"
	"quizzivy/internal/shared/visibility"
)

// readers lists the accounts the message m of the conversation v is for: the
// other participant of a direct thread, or the class's student members for
// an announcement, enabled, and with no read pointer that has reached m.
var readers = `coalesce(array(
	  SELECT r.id::text FROM app.users r
	   WHERE r.disabled_at IS NULL AND r.id <> m.sender_id
	     AND CASE WHEN v.kind = 'direct' THEN r.id IN (v.staff_id, v.student_id)
	              ELSE ` + visibility.StudentLike("r") + `
	                   AND EXISTS (SELECT 1 FROM app.class_members cm WHERE cm.class_id = v.class_id AND cm.user_id = r.id) END
	     AND NOT EXISTS (SELECT 1 FROM app.conversation_reads p
	                      WHERE p.conversation_id = v.id AND p.user_id = r.id AND p.last_read_message_id >= m.id)
	   ORDER BY r.id), '{}')`

// Delivery reads what the message messageID tells its thread's readers
// (domain.Repository), or domain.ErrNotFound for no such message.
func (p *Postgres) Delivery(ctx context.Context, messageID string) (domain.Delivery, error) {
	var d domain.Delivery
	var kind string
	var classID, className, staffID, studentID *string
	err := p.QueryRow(ctx, `
		SELECT v.id::text, v.kind, v.class_id::text, c.name, v.staff_id::text, v.student_id::text, `+nameOf("s")+`,
		       m.body, a.original_filename, `+readers+`
		  FROM app.messages m
		  JOIN app.conversations v ON v.id = m.conversation_id
		  JOIN app.users s ON s.id = m.sender_id
		  LEFT JOIN app.message_attachments a ON a.id = m.attachment_id
		  LEFT JOIN app.classes c ON c.id = v.class_id
		 WHERE m.id = $1::uuid`, messageID).
		Scan(&d.ConversationID, &kind, &classID, &className, &staffID, &studentID, &d.SenderName, &d.Body, &d.Filename, &d.Readers)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Delivery{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Delivery{}, fmt.Errorf("messaging: delivery: %w", err)
	}
	d.Kind, d.ClassID, d.ClassName, d.StaffID, d.StudentID = domain.Kind(kind), deref(classID), deref(className), deref(staffID), deref(studentID)
	return d, nil
}

// StaffUnread counts the messages past scope.UserID's read pointers, not
// counting their own, over their direct threads and the channels of the
// classes scope reaches that are not archived (domain.Repository): the sum of
// the unread counts of the staff list's rows.
func (p *Postgres) StaffUnread(ctx context.Context, scope access.Scope) (int, error) {
	var n int
	err := p.QueryRow(ctx, `
		SELECT coalesce(sum(t.unread), 0)::int FROM (
		  SELECT `+unread+` AS unread
		    FROM app.classes c
		    JOIN app.conversations v ON v.kind = 'class' AND v.class_id = c.id
		   WHERE c.archived_at IS NULL AND ($2::boolean OR c.teacher_id = $1::uuid)
		  UNION ALL
		  SELECT `+unread+`
		    FROM app.conversations v
		   WHERE v.kind = 'direct' AND v.staff_id = $1::uuid
		) t`, opt.String(scope.UserID), scope.All).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("messaging: staff unread: %w", err)
	}
	return n, nil
}

// StudentUnread counts the messages past studentID's read pointers, not
// counting their own, over their direct threads and the channels of the
// classes they are in now that are not archived, and 0 for an account that
// is not a student (domain.Repository): the sum of the unread counts of the
// student's Messages.
func (p *Postgres) StudentUnread(ctx context.Context, studentID string) (int, error) {
	var n int
	err := p.QueryRow(ctx, `
		SELECT CASE WHEN EXISTS (SELECT 1 FROM app.users s WHERE s.id = $1::uuid AND `+visibility.StudentLike("s")+`)
		       THEN (SELECT coalesce(sum(t.unread), 0)::int FROM (
		         SELECT `+unread+` AS unread
		           FROM app.conversations v
		          WHERE v.kind = 'direct' AND v.student_id = $1::uuid
		         UNION ALL
		         SELECT `+unread+`
		           FROM app.class_members cm
		           JOIN app.classes c ON c.id = cm.class_id AND c.archived_at IS NULL
		           JOIN app.conversations v ON v.kind = 'class' AND v.class_id = c.id
		          WHERE cm.user_id = $1::uuid) t)
		       ELSE 0 END`, opt.String(studentID)).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("messaging: student unread: %w", err)
	}
	return n, nil
}
