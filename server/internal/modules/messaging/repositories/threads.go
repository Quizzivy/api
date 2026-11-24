package repositories

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"quizzivy/internal/modules/messaging/domain"
	"quizzivy/internal/platform/db"
	"quizzivy/internal/shared/opt"
	"quizzivy/internal/shared/visibility"
)

func nameOf(alias string) string {
	return fmt.Sprintf(`coalesce(nullif(%[1]s.display_name, ''), %[1]s.full_name)`, alias)
}

func folded(expr string) string { return `app.immutable_unaccent(lower(` + expr + `))` }

func contains(expr string, param int) string {
	return fmt.Sprintf(`%s LIKE '%%' || app.immutable_unaccent(lower($%d)) || '%%' ESCAPE '\'`, folded(expr), param)
}

// The SQL over a student row u and the staff member in $1: the caller's
// classes the student is in now; the student's other current classes, those
// not archived that the caller does not teach (otherClassNames: names only,
// never an id, so nothing opens a class outside the caller's scope); and
// whether the student was taught by the caller and is in none of the
// caller's classes now. The list's search is in $2 and scope.all, which
// widens the channels only, in $3.
const (
	taughtNow = `SELECT c.id, c.name FROM app.class_members cm JOIN app.classes c ON c.id = cm.class_id AND c.teacher_id = $1::uuid
	  WHERE cm.user_id = u.id`
	otherNow = `SELECT c.id, c.name FROM app.class_members cm
	  JOIN app.classes c ON c.id = cm.class_id AND c.archived_at IS NULL AND c.teacher_id IS DISTINCT FROM $1::uuid
	  WHERE cm.user_id = u.id`
	classNames      = `coalesce((SELECT array_agg(k.name ORDER BY app.immutable_unaccent(lower(k.name)), k.id) FROM (` + taughtNow + `) k), '{}')`
	otherClassNames = `coalesce((SELECT array_agg(k.name ORDER BY app.immutable_unaccent(lower(k.name)), k.id) FROM (` + otherNow + `) k), '{}')`
	former          = `(EXISTS (SELECT 1 FROM app.messaging_ties t WHERE t.staff_id = $1::uuid AND t.student_id = u.id)
	   AND NOT EXISTS (` + taughtNow + `))`
)

// studentMatches is the list's search over a student row u: their names, or
// the name of one of the classes the row shows, the caller's or the
// student's other current ones.
func studentMatches(param int) string {
	return fmt.Sprintf(`(%s OR %s OR EXISTS (SELECT 1 FROM (%s UNION ALL %s) k WHERE %s))`,
		contains(`u.full_name`, param), contains(`coalesce(u.display_name, '')`, param), taughtNow, otherNow, contains(`k.name`, param))
}

// unread counts the messages of the conversation v that are past the pointer
// of the reader in $1, not counting the reader's own.
const unread = `(SELECT count(*)::int FROM app.messages um
	  WHERE um.conversation_id = v.id AND um.sender_id <> $1::uuid
	    AND um.id > coalesce((SELECT r.last_read_message_id FROM app.conversation_reads r
	                           WHERE r.conversation_id = v.id AND r.user_id = $1::uuid), '00000000-0000-0000-0000-000000000000'::uuid))`

const newest = `LEFT JOIN LATERAL (
	    SELECT lm.body, la.original_filename, lm.sender_id = $1::uuid AS mine
	      FROM app.messages lm LEFT JOIN app.message_attachments la ON la.id = lm.attachment_id
	     WHERE lm.conversation_id = v.id ORDER BY lm.id DESC LIMIT 1) last ON true`

func channelRows() string {
	return `
	SELECT 'class', c.id::text, NULL::text, c.name, NULL::text, '{}'::text[], '{}'::text[], ` + activeStudents("c.id") + `,
	       false, false, v.last_message_at, last.body, last.original_filename, coalesce(last.mine, false),
	       CASE WHEN v.id IS NULL THEN 0 ELSE ` + unread + ` END
	  FROM app.classes c
	  LEFT JOIN app.conversations v ON v.kind = 'class' AND v.class_id = c.id
	  ` + newest + `
	 WHERE c.archived_at IS NULL AND ($3::boolean OR c.teacher_id = $1::uuid)
	   AND ($2::text = '' OR ` + contains("c.name", 2) + `)`
}

func directRows() string {
	return `
	SELECT 'direct', NULL::text, u.id::text, ` + nameOf("u") + `, u.avatar_key, ` + classNames + `, ` + otherClassNames + `, NULL::int,
	       ` + former + `, u.disabled_at IS NOT NULL, v.last_message_at, last.body, last.original_filename, coalesce(last.mine, false),
	       ` + unread + `
	  FROM app.conversations v
	  JOIN app.users u ON u.id = v.student_id
	  ` + newest + `
	 WHERE v.kind = 'direct' AND v.staff_id = $1::uuid
	   AND ($2::text = '' OR ` + studentMatches(2) + `)`
}

func candidateRows() string {
	return `
	SELECT 'direct', NULL::text, u.id::text, ` + nameOf("u") + `, u.avatar_key, ` + classNames + `, ` + otherClassNames + `, NULL::int,
	       ` + former + `, false, NULL::timestamptz, NULL::text, NULL::text, false, 0
	  FROM app.users u
	 WHERE ` + visibility.CanMessage("u", 1) + `
	   AND NOT EXISTS (SELECT 1 FROM app.conversations v WHERE v.kind = 'direct' AND v.staff_id = $1::uuid AND v.student_id = u.id)
	   AND ` + studentMatches(2) + `
	 ORDER BY ` + folded(nameOf("u")) + `, u.id
	 LIMIT ` + fmt.Sprint(domain.MaxCandidates)
}

// Threads returns the staff list's rows for f: every class channel of a class
// f reaches that is not archived, posted in or not, and the caller's direct
// threads, newest activity first, a channel nobody has posted in last by
// name; then, with a query and a tab that lists direct threads, at most
// MaxCandidates students the caller may write to and has no thread with, by
// name: any enabled student, in one of the caller's classes, in another
// teacher's or in none (F2). A query matches the class's name, or the
// student's names and the names of the classes the row shows (the caller's
// and the student's other current ones), accent-insensitively.
func (p *Postgres) Threads(ctx context.Context, f domain.ThreadFilter) ([]domain.StaffThread, error) {
	query := strings.TrimSpace(f.Query)
	args := []any{opt.String(f.ActorID), db.EscapeLike(query)}
	var parts []string
	threadArgs := args
	if f.Tab.Channels() {
		parts = append(parts, channelRows())
		threadArgs = append(threadArgs, f.All)
	}
	if f.Tab.Directs() {
		parts = append(parts, directRows())
	}
	var out []domain.StaffThread
	if len(parts) > 0 {
		rows, err := db.QueryMany(ctx, p, `SELECT * FROM (`+strings.Join(parts, "\n	UNION ALL")+`
		) row (kind, class_id, student_id, title, avatar_key, class_names, other_class_names, student_count, former, disabled, last_message_at, body, filename, mine, unread)
		 ORDER BY row.last_message_at DESC NULLS LAST, `+folded("row.title")+`, coalesce(row.class_id, row.student_id)`, threadArgs, scanThread(false))
		if err != nil {
			return nil, fmt.Errorf("messaging: threads: %w", err)
		}
		out = rows
	}
	if query != "" && f.Tab.Directs() {
		candidates, err := db.QueryMany(ctx, p, candidateRows(), args, scanThread(true))
		if err != nil {
			return nil, fmt.Errorf("messaging: candidates: %w", err)
		}
		out = append(out, candidates...)
	}
	return out, nil
}

func scanThread(candidate bool) func(pgx.Rows) (domain.StaffThread, error) {
	return func(rows pgx.Rows) (domain.StaffThread, error) {
		var t domain.StaffThread
		var kind string
		var classID, studentID, body, filename *string
		var at *time.Time
		var mine bool
		if err := rows.Scan(&kind, &classID, &studentID, &t.Title, &t.AvatarKey, &t.ClassNames, &t.OtherClassNames, &t.StudentCount,
			&t.Former, &t.Disabled, &at, &body, &filename, &mine, &t.Unread); err != nil {
			return domain.StaffThread{}, err
		}
		t.Ref = domain.ThreadRef{Kind: domain.Kind(kind), ClassID: deref(classID), StudentID: deref(studentID)}
		t.Candidate, t.LastMessageAt = candidate, at
		if at != nil {
			t.Preview = domain.PreviewOf(body, filename, mine)
		}
		if t.ClassNames == nil {
			t.ClassNames = []string{}
		}
		if t.OtherClassNames == nil {
			t.OtherClassNames = []string{}
		}
		return t, nil
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// Peer reads studentID as the header of staffID's thread with them shows the
// student: the name, the photo key, the caller's classes the student is in
// now, the names of the student's other current classes, whether the student
// was taught by the caller and is in none of their classes now, and whether
// the account is disabled.
func (p *Postgres) Peer(ctx context.Context, staffID, studentID string) (domain.Peer, error) {
	var peer domain.Peer
	var classes []byte
	err := p.QueryRow(ctx, `
		SELECT u.id::text, `+nameOf("u")+`, u.avatar_key,
		       coalesce((SELECT jsonb_agg(jsonb_build_object('id', k.id, 'name', k.name) ORDER BY app.immutable_unaccent(lower(k.name)), k.id)
		                   FROM (`+taughtNow+`) k), '[]'::jsonb),
		       `+otherClassNames+`, `+former+`, u.disabled_at IS NOT NULL
		  FROM app.users u
		 WHERE u.id = $2::uuid`, opt.String(staffID), studentID).
		Scan(&peer.ID, &peer.Name, &peer.AvatarKey, &classes, &peer.OtherClassNames, &peer.Former, &peer.Disabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Peer{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Peer{}, fmt.Errorf("messaging: peer: %w", err)
	}
	if err := json.Unmarshal(classes, &peer.Classes); err != nil {
		return domain.Peer{}, fmt.Errorf("messaging: peer classes: %w", err)
	}
	if peer.OtherClassNames == nil {
		peer.OtherClassNames = []string{}
	}
	return peer, nil
}
