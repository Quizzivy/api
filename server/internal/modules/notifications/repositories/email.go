package repositories

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"quizzivy/internal/modules/notifications/domain"
	"quizzivy/internal/platform/db"
	"quizzivy/internal/platform/email"
	"quizzivy/internal/shared/answered"
	"quizzivy/internal/shared/schedule"
	"quizzivy/internal/shared/visibility"
)

// Mail is the notifications context's email store (T-R7.13): it writes a
// notification email into R5's outbox when its recipient's switch lets it,
// and reads what each email says when the dispatcher is about to send it. Its
// reads cross into the tables of the modules the emails speak of, as Due's
// do, and each judges what the recipient reaches as that module's read does
// (shared/visibility, shared/schedule).
type Mail struct {
	db.Repository
	outbox *email.Outbox
}

// NewMail is the email store over dbx, writing into outbox. A store that only
// prepares, as the dispatcher's, takes a nil outbox.
func NewMail(dbx db.Context, outbox *email.Outbox) *Mail {
	return &Mail{Repository: db.NewRepository(dbx), outbox: outbox}
}

var recipient = `
	SELECT u.email, coalesce(nullif(u.display_name, ''), u.full_name),
	       CASE WHEN ` + visibility.StudentLike("u") + ` THEN coalesce(p.in_app, $3::boolean)
	            ELSE coalesce(p.email, $4::boolean) END
	  FROM app.users u
	  LEFT JOIN app.notification_preferences p ON p.user_id = u.id AND p.event = $2::text
	 WHERE u.id = $1::uuid AND u.disabled_at IS NULL`

// decide reads userID's address and name and whether the email of the switch
// sw is on for them: a student's in-app switch (DG-82), anyone else's email
// column, the deck's default where nothing is saved, and on for an email no
// switch governs. A disabled or missing account gets none.
func decide(ctx context.Context, q db.Querier, userID string, sw *domain.Event) (address, name string, sends bool, err error) {
	var event *string
	student, staff := true, true
	if sw != nil {
		event = new(string(*sw))
		student, staff = domain.DefaultPreference(*sw, domain.Student).Email, domain.DefaultPreference(*sw, domain.Staff).Email
	}
	err = q.QueryRow(ctx, recipient, userID, event, student, staff).Scan(&address, &name, &sends)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, fmt.Errorf("notifications: email recipient: %w", err)
	}
	return address, name, sends, nil
}

// Recipient answers userID's name and whether the email of the switch sw is
// on for them now, for the emails' Preparers.
func (m *Mail) Recipient(ctx context.Context, userID string, sw *domain.Event) (string, bool, error) {
	_, name, sends, err := decide(ctx, m, userID, sw)
	return name, sends, err
}

// Enqueue writes e in one transaction with the reads that decide it
// (application/email.Outbox): nothing when its switch is off for its
// recipient, their address otherwise, and e's ids as the template's params.
// A digest whose bucket already has its row adds its paper to that row's
// (gather). A message's email writes nothing while an email about the
// thread waits for the reader, is keyed by the last one closed, so that two
// concurrent messages write one, and is held until
// domain.MessageEmailSpacing after the last one sent.
func (m *Mail) Enqueue(ctx context.Context, e domain.Email) (bool, error) {
	if m.outbox == nil {
		return false, errors.New("notifications: an email store without an outbox cannot enqueue")
	}
	params, err := paramsOf(e)
	if err != nil {
		return false, err
	}
	msg := email.Mail{ToUserID: e.UserID, Params: params, DedupeKey: e.DedupeKey, DeliverAfter: e.DeliverAfter}
	var wrote bool
	err = m.InTx(ctx, "notifications: enqueue email", func(tx pgx.Tx) (err error) {
		wrote, err = m.write(ctx, tx, e, msg)
		return err
	})
	if err != nil {
		return false, err
	}
	if wrote {
		m.outbox.Notify()
	}
	return wrote, nil
}

// write is Enqueue's transaction: msg, as e's email, to its recipient when
// their switch lets it.
func (m *Mail) write(ctx context.Context, tx pgx.Tx, e domain.Email, msg email.Mail) (bool, error) {
	address, _, sends, err := decide(ctx, tx, e.UserID, e.Switch)
	if err != nil || !sends {
		return false, err
	}
	msg.To = address
	if e.Thread != "" {
		if free, err := spaced(ctx, tx, e.UserID, e.Thread, &msg); err != nil || !free {
			return false, err
		}
	}
	id, wrote, err := m.outbox.Enqueue(ctx, tx, msg)
	if err != nil || wrote || e.Template != domain.EmailSubmissionsDigest {
		return wrote, err
	}
	return m.gather(ctx, tx, id, e.IDs.AttemptID, msg)
}

// paramsOf is e's ids as its template's params, which the outbox stores.
func paramsOf(e domain.Email) (email.Params, error) {
	ids := e.IDs
	switch e.Template {
	case domain.EmailAttemptFlagged:
		return email.AttemptFlaggedParams{AttemptID: ids.AttemptID}, nil
	case domain.EmailAssignmentExtended:
		return email.AssignmentExtendedParams{AssignmentID: ids.AssignmentID}, nil
	case domain.EmailResultReady:
		return email.ResultReadyParams{AttemptID: ids.AttemptID}, nil
	case domain.EmailContentShared:
		return email.ContentSharedParams{Kind: ids.ContentKind, ResourceID: ids.ResourceID}, nil
	case domain.EmailClassTeacherChanged:
		return email.ClassTeacherChangedParams{ClassID: ids.ClassID, Previous: ids.Previous}, nil
	case domain.EmailSubmissionsDigest:
		return email.SubmissionsDigestParams{AssignmentID: ids.AssignmentID, From: ids.From, AttemptIDs: []string{ids.AttemptID}}, nil
	case domain.EmailMessageReceived:
		return email.MessageReceivedParams{ConversationID: ids.ConversationID, StudentID: ids.StudentID, TeacherID: ids.TeacherID, ClassID: ids.ClassID}, nil
	}
	return nil, fmt.Errorf("notifications: %s is not an email Notify sends", e.Template)
}

// gather adds attemptID to the digest row id, which its bucket's first paper
// wrote, while that row waits, and answers false: the row counts the papers
// it names when it is sent. A paper it already names is not added twice. It
// waits for a dispatcher that holds the row. When the row was already sent or
// dropped without it, which a paper announced at the bucket's very end can
// find, the paper gets a row of its own beside it, keyed by the paper, that
// counts that paper alone.
func (m *Mail) gather(ctx context.Context, tx pgx.Tx, id, attemptID string, msg email.Mail) (bool, error) {
	tag, err := tx.Exec(ctx, `
		UPDATE app.email_outbox
		   SET params = jsonb_set(params, '{attemptIds}', coalesce(params->'attemptIds', '[]'::jsonb) || to_jsonb($2::text))
		 WHERE id = $1::uuid AND sent_at IS NULL AND failed_at IS NULL
		   AND NOT coalesce(params->'attemptIds', '[]'::jsonb) ? $2`, id, attemptID)
	if err != nil {
		return false, fmt.Errorf("notifications: add a paper to its digest: %w", err)
	}
	if tag.RowsAffected() == 1 {
		return false, nil
	}
	var named bool
	if err := tx.QueryRow(ctx, `SELECT coalesce(params->'attemptIds', '[]'::jsonb) ? $2 FROM app.email_outbox WHERE id = $1::uuid`, id, attemptID).Scan(&named); err != nil {
		return false, fmt.Errorf("notifications: a digest's papers: %w", err)
	}
	if named {
		return false, nil
	}
	msg.DedupeKey += ":" + attemptID
	_, wrote, err := m.outbox.Enqueue(ctx, tx, msg)
	return wrote, err
}

func spaced(ctx context.Context, tx pgx.Tx, userID, thread string, msg *email.Mail) (bool, error) {
	var last string
	var sentAt *time.Time
	var waiting bool
	err := tx.QueryRow(ctx, `
		SELECT o.id::text, o.sent_at, o.sent_at IS NULL AND o.failed_at IS NULL
		  FROM app.email_outbox o
		 WHERE o.to_user_id = $1::uuid AND o.template = $2 AND o.params->>'conversationId' = $3
		 ORDER BY o.created_at DESC, o.id DESC
		 LIMIT 1`, userID, string(email.MessageReceived), thread).Scan(&last, &sentAt, &waiting)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		last = "first"
	case err != nil:
		return false, fmt.Errorf("notifications: a thread's last email: %w", err)
	case waiting:
		return false, nil
	}
	msg.DedupeKey += ":" + last
	if sentAt != nil && msg.DeliverAfter.Before(sentAt.Add(domain.MessageEmailSpacing)) {
		msg.DeliverAfter = sentAt.Add(domain.MessageEmailSpacing)
	}
	return true, nil
}

// one runs a read that fills one email and answers false, not an error, when
// it finds no row.
func one(err error, what string) (bool, error) {
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("notifications: %s email: %w", what, err)
	}
	return true, nil
}

// Flagged reads the flagged paper's student, test and count of times the
// student left it, while the paper is still flagged, not voided, and one
// userID reaches (visibility.PaperReaders).
func (m *Mail) Flagged(ctx context.Context, userID string, p email.AttemptFlaggedParams, _ time.Time) (email.AttemptFlaggedParams, bool, error) {
	ok, err := one(m.QueryRow(ctx, `
		SELECT s.full_name, t.title, at.focus_loss_count
		  FROM app.attempts at
		  JOIN app.assignments a ON a.id = at.assignment_id
		  JOIN app.tests t ON t.id = a.test_id
		  JOIN app.users s ON s.id = at.student_id
		 WHERE at.id = $2::uuid AND at.status <> 'voided' AND at.flagged
		   AND $1::uuid IN `+visibility.PaperReaders("at.assignment_id", "at.student_id"),
		userID, p.AttemptID).Scan(&p.StudentName, &p.Title, &p.FocusLost), "flagged")
	return p, ok, err
}

// ownClose is a student's assignment as the email of their own close reads
// it: published, addressed to them, and still open at $3.
var ownClose = `
	SELECT t.title, ` + schedule.CloseOf("o") + `
	  FROM app.assignments a
	  JOIN app.tests t ON t.id = a.test_id
	  ` + schedule.OverrideJoin("$1::uuid") + `
	 WHERE a.id = $2::uuid AND a.published_at IS NOT NULL
	   AND $1::uuid IN ` + visibility.Roster("a.id") + `
	   AND ` + schedule.CloseOf("o") + ` > $3::timestamptz`

// Extended reads the test's title and userID's own close, while the
// assignment is published, addressed to them and still open.
func (m *Mail) Extended(ctx context.Context, userID string, p email.AssignmentExtendedParams, now time.Time) (email.AssignmentExtendedParams, bool, error) {
	ok, err := one(m.QueryRow(ctx, ownClose, userID, p.AssignmentID, now).Scan(&p.Title, &p.ClosesAt), "extension")
	return p, ok, err
}

// DueSoon reads the test's title and userID's own close, while the
// assignment is still open to them, not handed in and has an attempt left:
// T-R4.10b's rule, which drops a reminder that went stale while it waited.
func (m *Mail) DueSoon(ctx context.Context, userID string, p email.AssignmentDueSoonParams, now time.Time) (email.AssignmentDueSoonParams, bool, error) {
	ok, err := one(m.QueryRow(ctx, ownClose+`
	   AND NOT EXISTS (SELECT 1 FROM app.attempts at
	                    WHERE at.assignment_id = a.id AND at.student_id = $1::uuid
	                      AND (at.status IN ('submitted','timed_out','graded')
	                           OR (at.status = 'in_progress' AND at.deadline_at <= $3::timestamptz)))
	   AND (SELECT count(*) FROM app.attempts at
	         WHERE at.assignment_id = a.id AND at.student_id = $1::uuid AND at.status <> 'voided')
	       < a.max_attempts + coalesce(o.extra_attempts, 0)`,
		userID, p.AssignmentID, now).Scan(&p.Title, &p.ClosesAt), "reminder")
	return p, ok, err
}

// Closing reads the test's title, the assignment's close and how many of the
// students userID reaches have not handed in, while it is still open and at
// least one has not.
func (m *Mail) Closing(ctx context.Context, userID string, p email.AssignmentClosingParams, now time.Time) (email.AssignmentClosingParams, bool, error) {
	ok, err := one(m.QueryRow(ctx, `
		SELECT t.title, `+schedule.CloseOf("")+`, waiting.n::int
		  FROM app.assignments a
		  JOIN app.tests t ON t.id = a.test_id
		  CROSS JOIN LATERAL (
		       SELECT count(*) AS n
		         FROM `+visibility.Roster("a.id")+` r
		         JOIN app.users s ON s.id = r.user_id AND s.disabled_at IS NULL AND `+visibility.StudentLike("s")+`
		         LEFT JOIN app.assignment_student_overrides o ON o.assignment_id = a.id AND o.student_id = s.id
		        WHERE `+visibility.Papers(4, 1, "a.id", "s.id")+`
		          AND `+schedule.CloseOf("o")+` = `+schedule.CloseOf("")+`
		          AND NOT EXISTS (SELECT 1 FROM app.attempts at
		                           WHERE at.assignment_id = a.id AND at.student_id = s.id AND `+handedIn+`)) waiting
		 WHERE a.id = $3::uuid AND a.published_at IS NOT NULL AND a.id IN `+visibility.AssignmentIDs(1)+`
		   AND `+schedule.CloseOf("")+` > $2::timestamptz AND waiting.n > 0`,
		userID, now, p.AssignmentID, false).Scan(&p.Title, &p.ClosesAt, &p.NotSubmitted), "closing")
	return p, ok, err
}

// Ready reads the test's title while the paper is userID's, graded, and its
// review policy shows the score now. The email names no score.
func (m *Mail) Ready(ctx context.Context, userID string, p email.ResultReadyParams, now time.Time) (email.ResultReadyParams, bool, error) {
	ok, err := one(m.QueryRow(ctx, `
		SELECT t.title
		  FROM app.attempts at
		  JOIN app.assignments a ON a.id = at.assignment_id
		  JOIN app.tests t ON t.id = a.test_id
		  `+schedule.OverrideJoin("at.student_id")+`
		 WHERE at.id = $2::uuid AND at.student_id = $1::uuid AND at.status = 'graded'
		   AND a.review_show_score AND (a.review_release = 'on_submit' OR $3::timestamptz >= `+schedule.CloseOf("o")+`)`,
		userID, p.AttemptID, now).Scan(&p.Title), "result")
	return p, ok, err
}

// Shared reads the grant that shared p's content with userID: who granted it,
// the content's name, the access and the sharer's message, while the grant
// stands and the content is not deleted.
func (m *Mail) Shared(ctx context.Context, userID string, p email.ContentSharedParams, _ time.Time) (email.ContentSharedParams, bool, error) {
	ok, err := one(m.QueryRow(ctx, `
		SELECT coalesce(nullif(g.display_name, ''), g.full_name), coalesce(t.title, qs.name), cs.access, cs.message
		  FROM app.content_shares cs
		  JOIN app.users g ON g.id = cs.granted_by
		  LEFT JOIN app.tests t ON t.id = cs.test_id AND t.deleted_at IS NULL
		  LEFT JOIN app.question_sets qs ON qs.id = cs.question_set_id AND qs.deleted_at IS NULL
		 WHERE cs.grantee_id = $1::uuid AND cs.kind = $2
		   AND $3::uuid IN (cs.test_id, cs.question_set_id)
		   AND coalesce(t.id, qs.id) IS NOT NULL`,
		userID, p.Kind, p.ResourceID).Scan(&p.SharerName, &p.Name, &p.Access, &p.Message), "share")
	return p, ok, err
}

// TeacherChanged reads the class's name and, for its previous teacher, who
// teaches it now, while the change still holds: the new teacher still
// teaches it, and the previous one does not again.
func (m *Mail) TeacherChanged(ctx context.Context, userID string, p email.ClassTeacherChangedParams, _ time.Time) (email.ClassTeacherChangedParams, bool, error) {
	var teacherName *string
	var teaches bool
	ok, err := one(m.QueryRow(ctx, `
		SELECT c.name, coalesce(nullif(n.display_name, ''), n.full_name), c.teacher_id IS NOT DISTINCT FROM $1::uuid
		  FROM app.classes c
		  LEFT JOIN app.users n ON n.id = c.teacher_id
		 WHERE c.id = $2::uuid`,
		userID, p.ClassID).Scan(&p.ClassName, &teacherName, &teaches), "change of teacher")
	if !ok || err != nil || teaches == p.Previous || (p.Previous && teacherName == nil) {
		return p, false, err
	}
	if p.Previous {
		p.TeacherName = *teacherName
	}
	return p, true, nil
}

// Digest counts the papers the digest names (p.AttemptIDs, those announced
// during its bucket) that are still handed in on the assignment and that
// userID reaches, and those with an answer still waiting for a mark, and
// reads the test's title. It answers false when none is left, or the
// assignment is gone.
func (m *Mail) Digest(ctx context.Context, userID string, p email.SubmissionsDigestParams, _ time.Time) (email.SubmissionsDigestParams, bool, error) {
	ok, err := one(m.QueryRow(ctx, `
		SELECT t.title, counted.papers::int, counted.waiting::int
		  FROM app.assignments a
		  JOIN app.tests t ON t.id = a.test_id
		  CROSS JOIN LATERAL (
		       SELECT count(*) AS papers,
		              count(*) FILTER (WHERE EXISTS (SELECT 1 FROM app.attempt_answers ans
		                                              WHERE ans.attempt_id = at.id AND `+answered.AwaitsGrade("ans")+`)) AS waiting
		         FROM app.attempts at
		        WHERE at.id = ANY($3::uuid[]) AND at.assignment_id = a.id AND at.status IN ('submitted', 'timed_out', 'graded')
		          AND $1::uuid IN `+visibility.PaperReaders("at.assignment_id", "at.student_id")+`) counted
		 WHERE a.id = $2::uuid AND counted.papers > 0`,
		userID, p.AssignmentID, p.AttemptIDs).Scan(&p.Title, &p.Count, &p.ToGrade), "digest")
	return p, ok, err
}

// Message reads, for a thread userID still reads, how many messages past
// their pointer others wrote, who wrote the newest of them and, for a
// channel, the class's name. It answers false once the reader has caught up,
// or when they no longer read the thread: a direct thread's two participants
// read it, and a channel's student members.
func (m *Mail) Message(ctx context.Context, userID string, p email.MessageReceivedParams, _ time.Time) (email.MessageReceivedParams, bool, error) {
	var className *string
	ok, err := one(m.QueryRow(ctx, `
		SELECT coalesce(nullif(s.display_name, ''), s.full_name), c.name, unread.n::int
		  FROM app.conversations v
		  LEFT JOIN app.classes c ON c.id = v.class_id
		  CROSS JOIN LATERAL (
		       SELECT coalesce((SELECT r.last_read_message_id FROM app.conversation_reads r
		                         WHERE r.conversation_id = v.id AND r.user_id = $1::uuid),
		                       '00000000-0000-0000-0000-000000000000'::uuid) AS pointer) seen
		  CROSS JOIN LATERAL (
		       SELECT count(*) AS n FROM app.messages um
		        WHERE um.conversation_id = v.id AND um.sender_id <> $1::uuid AND um.id > seen.pointer) unread
		  CROSS JOIN LATERAL (
		       SELECT nm.sender_id FROM app.messages nm
		        WHERE nm.conversation_id = v.id AND nm.sender_id <> $1::uuid AND nm.id > seen.pointer
		        ORDER BY nm.id DESC LIMIT 1) newest
		  JOIN app.users s ON s.id = newest.sender_id
		 WHERE v.id = $2::uuid AND unread.n > 0
		   AND CASE WHEN v.kind = 'direct' THEN $1::uuid IN (v.staff_id, v.student_id)
		            ELSE EXISTS (SELECT 1 FROM app.class_members cm JOIN app.users r ON r.id = cm.user_id
		                          WHERE cm.class_id = v.class_id AND cm.user_id = $1::uuid AND `+visibility.StudentLike("r")+`) END`,
		userID, p.ConversationID).Scan(&p.SenderName, &className, &p.Count), "message")
	if className != nil {
		p.ClassName = *className
	}
	return p, ok, err
}
