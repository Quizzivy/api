package maintenance

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	notificationsdomain "quizzivy/internal/modules/notifications/domain"
	"quizzivy/internal/platform/db"
	"quizzivy/internal/shared/retention"
)

const (
	studentPlaceholder = "Học viên đã ẩn danh"
	userPlaceholder    = "Người dùng đã ẩn danh"
)

const anonymiseUsers = `
	WITH targets AS (
	  SELECT u.id, u.full_name, u.display_name,
	         EXISTS (SELECT 1 FROM app.student_like_roles s WHERE s.id = u.role_id) AS student
	    FROM app.users u
	   WHERE u.id = ANY($1::uuid[]) AND ` + retention.NotAnonymous + `
	), names AS MATERIALIZED (
	  SELECT DISTINCT n AS name
	    FROM targets t CROSS JOIN LATERAL unnest(ARRAY[t.full_name, t.display_name]) AS n
	   WHERE n IS NOT NULL AND n <> ''
	), notices AS (
	  DELETE FROM app.notifications n
	   WHERE n.id IN (
	     SELECT m.id
	       FROM app.notifications m
	      CROSS JOIN LATERAL jsonb_path_query(m.params, 'strict $.** ? (@.type() == "string")') AS v
	       JOIN names ON names.name = (v #>> '{}'))
	  RETURNING 1
	), identities AS (
	  DELETE FROM app.user_identities WHERE user_id IN (SELECT id FROM targets)
	), sessions AS (
	  DELETE FROM app.refresh_tokens WHERE user_id IN (SELECT id FROM targets)
	), links AS (
	  UPDATE app.set_password_tokens SET revoked_at = now()
	   WHERE user_id IN (SELECT id FROM targets) AND used_at IS NULL AND revoked_at IS NULL
	), changed AS (
	  UPDATE app.users u
	     SET email = ` + retention.AnonymousEmail + `,
	         full_name = CASE WHEN t.student THEN $2::text ELSE $3::text END,
	         display_name = NULL, phone = NULL, avatar_key = NULL,
	         password_hash = NULL, must_change_password = false,
	         disabled_at = coalesce(u.disabled_at, now())
	    FROM targets t
	   WHERE u.id = t.id
	  RETURNING u.id
	), audited AS (
	  INSERT INTO app.audit_log (action, entity, entity_id, diff)
	  SELECT 'user.anonymized', 'users', id,
	         jsonb_strip_nulls(jsonb_build_object('structuredIdentityRemoved', true, 'reason', $4::text,
	           'cutoff', $5::timestamptz, 'databaseRole', current_user))
	    FROM changed
	)
	SELECT (SELECT count(*) FROM changed), (SELECT count(*) FROM notices)`

// pickThreads finds the threads of the accounts being anonymised that have
// exactly two participants, with both: a direct thread names a staff member
// and a student, a colleague thread (F2, 00180) two staff members, low-first.
// Each arm matches one column, so each reads its own index.
const pickThreads = `
	WITH targets AS (
	  SELECT u.id FROM app.users u WHERE u.id = ANY($1::uuid[]) AND ` + retention.NotAnonymous + `
	)
	SELECT v.id::text, v.staff_id::text, v.student_id::text
	  FROM app.conversations v
	 WHERE v.kind = 'direct' AND v.staff_id IN (SELECT id FROM targets)
	UNION
	SELECT v.id::text, v.staff_id::text, v.student_id::text
	  FROM app.conversations v
	 WHERE v.kind = 'direct' AND v.student_id IN (SELECT id FROM targets)
	UNION
	SELECT v.id::text, v.colleague_low_id::text, v.colleague_high_id::text
	  FROM app.conversations v
	 WHERE v.kind = 'colleague' AND v.colleague_low_id IN (SELECT id FROM targets)
	UNION
	SELECT v.id::text, v.colleague_low_id::text, v.colleague_high_id::text
	  FROM app.conversations v
	 WHERE v.kind = 'colleague' AND v.colleague_high_id IN (SELECT id FROM targets)`

const forgetTies = `
	DELETE FROM app.messaging_ties t
	 USING app.users u
	 WHERE u.id = ANY($1::uuid[]) AND ` + retention.NotAnonymous + `
	   AND (t.staff_id = u.id OR t.student_id = u.id)`

type anonymised struct {
	users   int64
	notices int64
}

func anonymise(ctx context.Context, tx pgx.Tx, ids []string, reason string, cutoff *time.Time) (anonymised, error) {
	var out anonymised
	if err := forgetThreads(ctx, tx, ids); err != nil {
		return out, err
	}
	err := tx.QueryRow(ctx, anonymiseUsers, ids, studentPlaceholder, userPlaceholder, reason, cutoff).Scan(&out.users, &out.notices)
	return out, err
}

func forgetThreads(ctx context.Context, tx pgx.Tx, ids []string) error {
	rows, err := tx.Query(ctx, pickThreads, ids)
	if err != nil {
		return fmt.Errorf("find direct and colleague threads: %w", err)
	}
	var threads, holders, keys []string
	var id, one, other string
	if _, err := pgx.ForEachRow(rows, []any{&id, &one, &other}, func() error {
		threads = append(threads, id)
		holders = append(holders, one, other)
		keys = append(keys, notificationsdomain.MessageKey(id), notificationsdomain.MessageKey(id))
		return nil
	}); err != nil {
		return fmt.Errorf("find direct and colleague threads: %w", err)
	}
	if len(threads) > 0 {
		rows, err := tx.Query(ctx, `DELETE FROM app.messages WHERE conversation_id = ANY($1::uuid[]) RETURNING attachment_id::text`, threads)
		if err != nil {
			return fmt.Errorf("delete the threads' messages: %w", err)
		}
		files, err := pgx.CollectRows(rows, pgx.RowTo[*string])
		if err != nil {
			return fmt.Errorf("delete the threads' messages: %w", err)
		}
		for _, statement := range []struct {
			what string
			sql  string
			args []any
		}{
			{"delete the messages' files", `DELETE FROM app.message_attachments WHERE id = ANY($1::uuid[])`, []any{files}},
			{"delete read pointers", `DELETE FROM app.conversation_reads WHERE conversation_id = ANY($1::uuid[])`, []any{threads}},
			{"delete the threads", `DELETE FROM app.conversations WHERE id = ANY($1::uuid[])`, []any{threads}},
			{"delete the threads' notifications", `DELETE FROM app.notifications n USING unnest($1::uuid[], $2::text[]) AS k(user_id, dedupe_key)
				WHERE n.user_id = k.user_id AND n.dedupe_key = k.dedupe_key`, []any{holders, keys}},
		} {
			if _, err := tx.Exec(ctx, statement.sql, statement.args...); err != nil {
				return fmt.Errorf("%s: %w", statement.what, err)
			}
		}
	}
	if _, err := tx.Exec(ctx, forgetTies, ids); err != nil {
		return fmt.Errorf("delete messaging ties: %w", err)
	}
	return nil
}

// AnonymizationReport names the account an anonymize-user run handled without exposing its previous identity.
type AnonymizationReport struct {
	UserID  string `json:"userId"`
	Applied bool   `json:"applied"`
}

// AnonymizeUser removes an account's structured identity and login credentials on an explicit request, as the sweep does for a long-disabled account: its name becomes a placeholder, its email a reserved address, its photo reference, phone and password are cleared, its Google link and refresh tokens are removed and its unused set-password links are revoked.
// Notifications that carry its name are deleted. Its direct threads and its colleague threads (F2) go in the same transaction (D17): their messages, read pointers and the rows of the files those messages carried, and the in-app items both participants hold for them, while the files' objects are left to the housekeeping's sweep of objects no row names, since this role holds no storage credentials; its messaging ties go too, so nobody finds it as a former student. Its posts in class channels stay, under the placeholder name. Attempts, memberships and the append-only audit trail stay for historical review. It refuses an account that owns classes or content, a question set included, or has an attempt in progress.
func AnonymizeUser(ctx context.Context, conn db.Conn, userID string, apply bool) (AnonymizationReport, error) {
	out := AnonymizationReport{UserID: userID}
	if _, err := uuid.Parse(userID); err != nil {
		return out, errors.New("user ID must be a UUID")
	}
	tx, err := conn.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var owns, busy, done bool
	err = tx.QueryRow(ctx, `SELECT `+retention.OwnsContent+`, `+retention.HasAttemptInProgress+`, u.email = `+retention.AnonymousEmail+`
		 FROM app.users u WHERE u.id = $1 FOR UPDATE OF u`, userID).Scan(&owns, &busy, &done)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, errors.New("no account has that ID")
	}
	if err != nil {
		return out, fmt.Errorf("find account: %w", err)
	}
	if !done {
		if owns {
			return out, errors.New("the account owns classes or content: transfer it to another teacher before anonymizing")
		}
		if busy {
			return out, errors.New("finish or void the account's active attempts before anonymizing")
		}
	}
	if !apply {
		return out, nil
	}
	if _, err := anonymise(ctx, tx, []string{userID}, "request", nil); err != nil {
		return out, fmt.Errorf("anonymize account: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return out, err
	}
	out.Applied = true
	return out, nil
}
