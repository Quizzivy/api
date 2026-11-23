package repositories

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"quizzivy/internal/modules/timetable/domain"
	"quizzivy/internal/platform/db"
	"quizzivy/internal/shared/access"
	"quizzivy/internal/shared/opt"
)

// activeMember is a live account's membership of a class, as the class
// members list reads it: a disabled account is no member of a register.
const activeMember = `SELECT 1 FROM app.class_members cm JOIN app.users cu ON cu.id = cm.user_id AND cu.disabled_at IS NULL`

func rowIDOf(f domain.Found) *string {
	if f.Row == nil {
		return nil
	}
	return &f.Row.ID
}

func classOf(f domain.Found) string {
	if f.Series != nil {
		return f.Series.ClassID
	}
	return f.Row.ClassID
}

func windowBounds(w domain.RateWindow) (*time.Time, *time.Time) {
	if w.Overall {
		return nil, nil
	}
	return &w.From, &w.To
}

// Roster reads the register of f in one query: the class's active members and
// the students marked on its row, each with their mark and their tally over
// the class's non-cancelled sessions that start in w.
func (p *Postgres) Roster(ctx context.Context, f domain.Found, w domain.RateWindow) ([]domain.RosterStudent, error) {
	from, to := windowBounds(w)
	out, err := db.QueryMany(ctx, p, `
		WITH roster AS (
		  SELECT m.user_id AS id FROM app.class_members m
		    JOIN app.users u ON u.id = m.user_id AND u.disabled_at IS NULL
		   WHERE m.class_id = $1::uuid
		  UNION
		  SELECT a.student_id FROM app.attendance_marks a WHERE a.session_id = $2::uuid
		), tallies AS (
		  SELECT a.student_id,
		         count(*) FILTER (WHERE a.status IN ('present', 'late')) AS attended,
		         count(*) AS counted
		    FROM app.attendance_marks a
		    JOIN app.class_sessions s ON s.id = a.session_id
		   WHERE s.class_id = $1::uuid AND s.cancelled_at IS NULL
		     AND ($3::timestamptz IS NULL OR s.starts_at >= $3)
		     AND ($4::timestamptz IS NULL OR s.starts_at < $4)
		     AND a.student_id IN (SELECT id FROM roster)
		   GROUP BY a.student_id
		)
		SELECT u.id::text, u.full_name, u.avatar_key, mk.status, mk.note,
		       coalesce(t.attended, 0)::int, coalesce(t.counted, 0)::int
		  FROM roster x
		  JOIN app.users u ON u.id = x.id
		  LEFT JOIN app.class_members cm ON cm.class_id = $1::uuid AND cm.user_id = x.id AND u.disabled_at IS NULL
		  LEFT JOIN app.attendance_marks mk ON mk.session_id = $2::uuid AND mk.student_id = x.id
		  LEFT JOIN tallies t ON t.student_id = x.id
		 -- The class members list's order, u.id breaking joined_at ties; a
		 -- marked student who is no active member comes after the members.
		 ORDER BY cm.joined_at DESC NULLS LAST, u.id DESC`,
		[]any{classOf(f), rowIDOf(f), from, to}, func(rows pgx.Rows) (domain.RosterStudent, error) {
			var s domain.RosterStudent
			var status *string
			err := rows.Scan(&s.StudentID, &s.FullName, &s.AvatarKey, &status, &s.Note, &s.Tally.Attended, &s.Tally.Counted)
			if status != nil {
				st := domain.Status(*status)
				s.Status = &st
			}
			return s, err
		})
	if err != nil {
		return nil, fmt.Errorf("timetable: the register of %s: %w", f.ID, err)
	}
	return out, nil
}

// SaveMarks writes marks under the class's lock, then the series', the order
// every timetable write takes them.
func (p *Postgres) SaveMarks(ctx context.Context, scope access.Scope, id domain.SessionID, check func(domain.Found) error, marks []domain.MarkInput, by domain.Writer) error {
	return p.InTx(ctx, "timetable: save attendance", func(tx pgx.Tx) error {
		f, err := lockSession(ctx, tx, scope, id)
		if err != nil {
			return err
		}
		if err := check(f); err != nil {
			return err
		}
		if err := refuseOffRoster(ctx, tx, f, marks); err != nil {
			return err
		}
		if f.Row == nil {
			if !domain.Changes(marks) {
				return nil
			}
			row, err := materialise(ctx, tx, *f.Series, f.ID.Date, by)
			if err != nil {
				return err
			}
			f.Row = &row
		}
		return writeMarks(ctx, tx, f, marks, by)
	})
}

func studentIDs(marks []domain.MarkInput) []string {
	out := make([]string, len(marks))
	for i, m := range marks {
		out[i] = m.StudentID
	}
	return out
}

// refuseOffRoster refuses the students of marks who are neither an active
// member of the session's class nor marked on its row, in the order sent.
func refuseOffRoster(ctx context.Context, tx pgx.Tx, f domain.Found, marks []domain.MarkInput) error {
	off, err := db.QueryMany(ctx, tx, `
		SELECT x.id::text
		  FROM unnest($1::uuid[]) WITH ORDINALITY AS x(id, n)
		 WHERE NOT EXISTS (`+activeMember+` WHERE cm.class_id = $2::uuid AND cm.user_id = x.id)
		   AND NOT EXISTS (SELECT 1 FROM app.attendance_marks a WHERE a.session_id = $3::uuid AND a.student_id = x.id)
		 ORDER BY x.n`, []any{studentIDs(marks), classOf(f), rowIDOf(f)}, func(r pgx.Rows) (string, error) {
		var id string
		err := r.Scan(&id)
		return id, err
	})
	if err != nil {
		return fmt.Errorf("timetable: the roster of %s: %w", f.ID, err)
	}
	if len(off) > 0 {
		return &domain.RefusedMarks{Reason: domain.RefusedOffRoster, StudentIDs: off}
	}
	return nil
}

// writeMarks writes marks on f's row and its attendance.saved row in one
// statement. A null status deletes the mark and its note; a status sets or
// changes it, with the note sent, or the stored one when none was; a mark
// whose status and note are already stored is not touched. The audit diff
// lists each student whose status changed, from and to (null for unmarked),
// and "note": "changed" for a note that changed, never its text. Nothing
// changed, no audit row.
func writeMarks(ctx context.Context, tx pgx.Tx, f domain.Found, marks []domain.MarkInput, by domain.Writer) error {
	n := len(marks)
	statuses, notes, noteSet := make([]*string, n), make([]*string, n), make([]bool, n)
	for i, m := range marks {
		if m.Status != nil {
			s := string(*m.Status)
			statuses[i] = &s
		}
		notes[i], noteSet[i] = m.Note.Value, m.Note.Set
	}
	_, err := tx.Exec(ctx, `
		WITH wanted AS (
		  SELECT w.student_id, w.status,
		         CASE WHEN w.note_set THEN w.note ELSE cur.note END AS note
		    FROM unnest($2::uuid[], $3::text[], $4::text[], $5::boolean[]) AS w(student_id, status, note, note_set)
		    LEFT JOIN app.attendance_marks cur ON cur.session_id = $1::uuid AND cur.student_id = w.student_id
		), removed AS (
		  DELETE FROM app.attendance_marks m USING wanted w
		   WHERE m.session_id = $1::uuid AND m.student_id = w.student_id AND w.status IS NULL
		  RETURNING m.student_id, m.status AS prev_status, NULL::text AS next_status, m.note IS NOT NULL AS note_changed
		), saved AS (
		  INSERT INTO app.attendance_marks AS m (session_id, student_id, status, marked_by, marked_at, note)
		  SELECT $1::uuid, w.student_id, w.status, $6::uuid, $7, w.note FROM wanted w WHERE w.status IS NOT NULL
		  ON CONFLICT (session_id, student_id) DO UPDATE
		     SET status = EXCLUDED.status, note = EXCLUDED.note,
		         marked_by = CASE WHEN m.status = EXCLUDED.status THEN m.marked_by ELSE EXCLUDED.marked_by END,
		         marked_at = CASE WHEN m.status = EXCLUDED.status THEN m.marked_at ELSE EXCLUDED.marked_at END
		   WHERE m.status IS DISTINCT FROM EXCLUDED.status OR m.note IS DISTINCT FROM EXCLUDED.note
		  RETURNING m.student_id, old.status AS prev_status, new.status AS next_status,
		            old.note IS DISTINCT FROM new.note AS note_changed
		), changes AS (
		  SELECT * FROM removed UNION ALL SELECT * FROM saved
		)
		INSERT INTO app.audit_log
		       (actor_user_id, action, entity, entity_id, occurred_at, ip, user_agent, diff)
		SELECT $6::uuid, 'attendance.saved', 'class_session', $1::uuid, $7, nullif($8, '')::inet, nullif($9, ''),
		       jsonb_build_object('sessionId', $10::text, 'classId', $11::uuid, 'changes', jsonb_agg(
		         jsonb_build_object('studentId', c.student_id)
		         || CASE WHEN c.prev_status IS DISTINCT FROM c.next_status
		                 THEN jsonb_build_object('from', c.prev_status, 'to', c.next_status) ELSE '{}'::jsonb END
		         || CASE WHEN c.note_changed THEN jsonb_build_object('note', 'changed') ELSE '{}'::jsonb END
		         ORDER BY c.student_id))
		  FROM changes c
		HAVING count(*) > 0`,
		f.Row.ID, studentIDs(marks), statuses, notes, noteSet, opt.String(by.ActorID), by.Now, by.IP, by.UserAgent,
		f.ID.String(), classOf(f))
	if err != nil {
		return fmt.Errorf("timetable: save the marks of %s: %w", f.ID, err)
	}
	return nil
}

// ClassReached reports whether scope reaches class id.
func (p *Postgres) ClassReached(ctx context.Context, scope access.Scope, id string) (bool, error) {
	ok, err := db.Exists(ctx, p, `SELECT 1 FROM app.classes c WHERE c.id = $3::uuid AND `+reached, scope.All, opt.String(scope.UserID), id)
	if err != nil {
		return false, fmt.Errorf("timetable: reach class %s: %w", id, err)
	}
	return ok, nil
}

// Earliest reads the first day of w's classes' timetable in one query.
func (p *Postgres) Earliest(ctx context.Context, w domain.Window) (*time.Time, error) {
	var out *time.Time
	err := p.QueryRow(ctx, `
		SELECT least(
		  (SELECT min(s.starts_on) FROM app.class_session_series s JOIN app.classes c ON c.id = s.class_id
		    WHERE `+reached+` AND ($3::uuid[] IS NULL OR s.class_id = ANY($3::uuid[]))),
		  (SELECT min(r.starts_at AT TIME ZONE 'UTC')::date FROM app.class_sessions r JOIN app.classes c ON c.id = r.class_id
		    WHERE `+reached+` AND ($3::uuid[] IS NULL OR r.class_id = ANY($3::uuid[]))))`,
		w.Scope.All, opt.String(w.Scope.UserID), w.ClassIDs).Scan(&out)
	if err != nil {
		return nil, fmt.Errorf("timetable: the first session: %w", err)
	}
	if out != nil {
		d := domain.Day(*out)
		out = &d
	}
	return out, nil
}

// Facts reads, in one query, how many students each session's register lists
// (the class's active members, and the students marked on its row who are
// not one) and its marks by status.
func (p *Postgres) Facts(ctx context.Context, sessions []domain.Session) (map[string]domain.SessionFacts, error) {
	var classes, rows []string
	seen := map[string]bool{}
	for _, s := range sessions {
		if !seen[s.ClassID] {
			seen[s.ClassID] = true
			classes = append(classes, s.ClassID)
		}
		if s.RowID != "" && s.HasAttendance {
			rows = append(rows, s.RowID)
		}
	}
	type fact struct {
		kind, id                     string
		count, present, late, absent int
	}
	found, err := db.QueryMany(ctx, p, `
		SELECT 'class', m.class_id::text, count(*)::int, 0, 0, 0
		  FROM app.class_members m JOIN app.users u ON u.id = m.user_id AND u.disabled_at IS NULL
		 WHERE m.class_id = ANY($1::uuid[])
		 GROUP BY m.class_id
		UNION ALL
		SELECT 'row', a.session_id::text,
		       count(*) FILTER (WHERE NOT EXISTS (`+activeMember+` WHERE cm.class_id = r.class_id AND cm.user_id = a.student_id))::int,
		       count(*) FILTER (WHERE a.status = 'present')::int,
		       count(*) FILTER (WHERE a.status = 'late')::int,
		       count(*) FILTER (WHERE a.status = 'absent')::int
		  FROM app.attendance_marks a JOIN app.class_sessions r ON r.id = a.session_id
		 WHERE a.session_id = ANY($2::uuid[])
		 GROUP BY a.session_id, r.class_id`, []any{classes, rows}, func(r pgx.Rows) (fact, error) {
		var f fact
		err := r.Scan(&f.kind, &f.id, &f.count, &f.present, &f.late, &f.absent)
		return f, err
	})
	if err != nil {
		return nil, fmt.Errorf("timetable: the register facts: %w", err)
	}
	members, marked := map[string]int{}, map[string]fact{}
	for _, f := range found {
		if f.kind == "class" {
			members[f.id] = f.count
		} else {
			marked[f.id] = f
		}
	}
	out := make(map[string]domain.SessionFacts, len(sessions))
	for _, s := range sessions {
		facts := domain.SessionFacts{StudentCount: members[s.ClassID]}
		if m, ok := marked[s.RowID]; ok && s.RowID != "" {
			facts.StudentCount += m.count
			facts.Counts = &domain.Counts{Present: m.present, Late: m.late, Absent: m.absent}
		}
		out[s.ID.String()] = facts
	}
	return out, nil
}
