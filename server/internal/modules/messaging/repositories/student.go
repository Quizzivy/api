package repositories

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"quizzivy/internal/modules/messaging/domain"
	"quizzivy/internal/platform/db"
	"quizzivy/internal/shared/opt"
	"quizzivy/internal/shared/visibility"
)

// The SQL of the student side, over a teacher row t and the student in $1:
// the student's classes the teacher teaches now that are not archived, and
// whether the teacher taught the student (app.messaging_ties) and teaches
// them in none of them now. An archived class lets no one write first (the
// owner, 2026-11-17), so it is neither the Teaches fact nor a class the
// subtitle names, as the student's class list and Messages leave it out. Its
// channel stays readable (StudentChannel) and a thread that exists stays open.
const (
	teachesNow = `SELECT c.id, c.name FROM app.class_members cm
	  JOIN app.classes c ON c.id = cm.class_id AND c.teacher_id = t.id AND c.archived_at IS NULL
	  WHERE cm.user_id = $1::uuid`
	teacherClasses = `coalesce((SELECT array_agg(k.name ORDER BY app.immutable_unaccent(lower(k.name)), k.id) FROM (` + teachesNow + `) k), '{}')`
	formerTeacher  = `(EXISTS (SELECT 1 FROM app.messaging_ties ft WHERE ft.staff_id = t.id AND ft.student_id = $1::uuid)
	   AND NOT EXISTS (` + teachesNow + `))`
)

// studentFactsSQL reads the facts of the student in $1 and the teacher in $2.
// A tie is no fact here: it tells a former teacher apart, and a student
// writes first only to a teacher of a class they are in now.
func studentFactsSQL() string {
	return `
	SELECT t.disabled_at IS NULL, NOT ` + visibility.StudentLike("t") + `,
	       coalesce((SELECT ` + visibility.StudentLike("s") + ` FROM app.users s WHERE s.id = $1::uuid), false),
	       EXISTS (SELECT 1 FROM app.conversations v WHERE v.kind = 'direct' AND v.staff_id = t.id AND v.student_id = $1::uuid),
	       EXISTS (` + teachesNow + `)
	  FROM app.users t
	 WHERE t.id = $2::uuid`
}

// StudentFacts reads what decides studentID's direct thread with teacherID.
func (p *Postgres) StudentFacts(ctx context.Context, studentID, teacherID string) (domain.StudentFacts, error) {
	return studentFacts(ctx, p, studentID, teacherID)
}

func studentFacts(ctx context.Context, q db.Querier, studentID, teacherID string) (domain.StudentFacts, error) {
	f := domain.StudentFacts{Exists: true, Self: studentID == teacherID}
	err := q.QueryRow(ctx, studentFactsSQL(), opt.String(studentID), teacherID).
		Scan(&f.TeacherEnabled, &f.TeacherStaff, &f.Student, &f.Conversation, &f.Teaches)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.StudentFacts{Self: f.Self}, nil
	}
	if err != nil {
		return domain.StudentFacts{}, fmt.Errorf("messaging: student facts: %w", err)
	}
	return f, nil
}

// Teacher reads teacherID as the header of studentID's thread with them shows
// the teacher.
func (p *Postgres) Teacher(ctx context.Context, studentID, teacherID string) (domain.Teacher, error) {
	var t domain.Teacher
	err := p.QueryRow(ctx, `
		SELECT t.id::text, `+nameOf("t")+`, t.avatar_key, `+teacherClasses+`, `+formerTeacher+`
		  FROM app.users t
		 WHERE t.id = $2::uuid`, opt.String(studentID), teacherID).
		Scan(&t.ID, &t.Name, &t.AvatarKey, &t.ClassNames, &t.Former)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Teacher{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Teacher{}, fmt.Errorf("messaging: teacher: %w", err)
	}
	if t.ClassNames == nil {
		t.ClassNames = []string{}
	}
	return t, nil
}

// StudentChannel reads the class classID for its member studentID, archived
// or not, with its teacher; ErrNotFound for anyone else.
func (p *Postgres) StudentChannel(ctx context.Context, studentID, classID string) (domain.StudentChannel, error) {
	var c domain.StudentChannel
	err := p.QueryRow(ctx, `
		SELECT c.id::text, c.name, t.id::text, `+nameOf("t")+`, t.avatar_key
		  FROM app.classes c
		  JOIN app.class_members cm ON cm.class_id = c.id AND cm.user_id = $2::uuid
		  JOIN app.users t ON t.id = c.teacher_id
		 WHERE c.id = $1::uuid`, classID, opt.String(studentID)).
		Scan(&c.ClassID, &c.Name, &c.Teacher.ID, &c.Teacher.Name, &c.Teacher.AvatarKey)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.StudentChannel{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.StudentChannel{}, fmt.Errorf("messaging: student channel: %w", err)
	}
	return c, nil
}

// StudentThreads returns studentID's Messages: their direct threads, and the
// channels of the classes they are in now that are not archived and have a
// post, newest activity first, then by title. A conversation exists only
// from its first post, so a channel with a conversation has a post.
func (p *Postgres) StudentThreads(ctx context.Context, studentID string) ([]domain.StudentThread, error) {
	rows, err := db.QueryMany(ctx, p, `SELECT * FROM (
	SELECT 'direct', NULL::text, t.id::text, `+nameOf("t")+`, t.avatar_key, `+teacherClasses+`, `+formerTeacher+`,
	       v.last_message_at, last.body, last.original_filename, coalesce(last.mine, false), `+unread+`
	  FROM app.conversations v
	  JOIN app.users t ON t.id = v.staff_id
	  `+newest+`
	 WHERE v.kind = 'direct' AND v.student_id = $1::uuid
	UNION ALL
	SELECT 'class', c.id::text, NULL::text, c.name, NULL::text, '{}'::text[], false,
	       v.last_message_at, last.body, last.original_filename, coalesce(last.mine, false), `+unread+`
	  FROM app.class_members cm
	  JOIN app.classes c ON c.id = cm.class_id AND c.archived_at IS NULL
	  JOIN app.conversations v ON v.kind = 'class' AND v.class_id = c.id
	  `+newest+`
	 WHERE cm.user_id = $1::uuid
	) row (kind, class_id, teacher_id, title, avatar_key, class_names, former, last_message_at, body, filename, mine, unread)
	 ORDER BY row.last_message_at DESC, `+folded("row.title")+`, coalesce(row.class_id, row.teacher_id)`,
		[]any{opt.String(studentID)}, func(rows pgx.Rows) (domain.StudentThread, error) {
			var t domain.StudentThread
			var kind string
			var classID, teacherID, body, filename *string
			var at time.Time
			var mine bool
			if err := rows.Scan(&kind, &classID, &teacherID, &t.Title, &t.AvatarKey, &t.ClassNames, &t.Former,
				&at, &body, &filename, &mine, &t.Unread); err != nil {
				return domain.StudentThread{}, err
			}
			t.Kind, t.ClassID, t.TeacherID = domain.Kind(kind), deref(classID), deref(teacherID)
			t.LastMessageAt = &at
			t.Preview = domain.PreviewOf(body, filename, mine)
			if t.ClassNames == nil {
				t.ClassNames = []string{}
			}
			return t, nil
		})
	if err != nil {
		return nil, fmt.Errorf("messaging: student threads: %w", err)
	}
	if rows == nil {
		rows = []domain.StudentThread{}
	}
	return rows, nil
}

// StudentChannelPage reads one page of the channel of classID as a student
// reads it: no post carries a SeenBy.
func (p *Postgres) StudentChannelPage(ctx context.Context, classID string, cursor domain.Cursor) (domain.Page, error) {
	conversation, err := classConversation(ctx, p, classID)
	if err != nil {
		return domain.Page{}, err
	}
	return p.page(ctx, conversation, "", cursor)
}

// FirstMessageAt is when the first message of staffID's thread with studentID
// was written, nil while the thread has none.
func (p *Postgres) FirstMessageAt(ctx context.Context, staffID, studentID string) (*time.Time, error) {
	var at time.Time
	err := p.QueryRow(ctx, `
		SELECT m.created_at FROM app.conversations v JOIN app.messages m ON m.conversation_id = v.id
		 WHERE v.kind = 'direct' AND v.staff_id = $1::uuid AND v.student_id = $2::uuid
		 ORDER BY m.id LIMIT 1`, opt.String(staffID), studentID).Scan(&at)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("messaging: first message: %w", err)
	}
	return &at, nil
}

// PostAsStudent writes post to studentID's thread with teacherID in one
// transaction (domain.Repository). It first holds the student's memberships
// of the teacher's classes FOR SHARE, so a removal from the class waits for a
// first post in flight, and one that committed before leaves the student no
// class to start from. The hold does not cover the class row: an archive
// (classes.archived_at, read by teachesNow) that commits while a first post
// is in flight does not wait for it. That leaves a thread started a moment
// before the archive, which the rule allows, as a thread outlives the
// archive; a lock on the class would make every rename and change of teacher
// wait as well.
func (p *Postgres) PostAsStudent(ctx context.Context, studentID, teacherID string, post domain.Post) (domain.Posted, error) {
	return p.post(ctx, "messaging: post as student", post, func(ctx context.Context, tx pgx.Tx) (target, error) {
		if _, err := tx.Exec(ctx, `
			SELECT 1 FROM app.class_members cm JOIN app.classes c ON c.id = cm.class_id AND c.teacher_id = $2::uuid
			 WHERE cm.user_id = $1::uuid FOR SHARE OF cm`, opt.String(studentID), teacherID); err != nil {
			return target{}, fmt.Errorf("messaging: hold memberships: %w", err)
		}
		facts, err := studentFacts(ctx, tx, studentID, teacherID)
		if err != nil {
			return target{}, err
		}
		rules := domain.Eligibility{}
		if err := rules.ReadAsStudent(facts); err != nil {
			return target{}, err
		}
		return target{
			find: func(ctx context.Context, tx pgx.Tx) (string, error) {
				return directConversation(ctx, tx, teacherID, studentID)
			},
			allow: func() error { return rules.PostAsStudent(facts) },
			open: func(ctx context.Context, tx pgx.Tx) error {
				if _, err := tx.Exec(ctx, `
					INSERT INTO app.conversations (kind, staff_id, student_id, last_message_at) VALUES ('direct', $1::uuid, $2::uuid, now())
					ON CONFLICT (staff_id, student_id) WHERE kind = 'direct' DO NOTHING`, teacherID, studentID); err != nil {
					return fmt.Errorf("messaging: open direct thread: %w", err)
				}
				return nil
			},
		}, nil
	})
}
