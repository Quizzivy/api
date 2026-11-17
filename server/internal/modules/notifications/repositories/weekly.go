package repositories

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"quizzivy/internal/modules/notifications/domain"
	"quizzivy/internal/platform/db"
	"quizzivy/internal/shared/answered"
	"quizzivy/internal/shared/visibility"
)

var weeklyTeachersQuery = `
SELECT u.id::text, coalesce(u.time_zone, $1::text),
       $2::boolean AND coalesce(p.email, $3::boolean), coalesce(p.in_app, $4::boolean)
  FROM app.users u
  LEFT JOIN app.notification_preferences p ON p.user_id = u.id AND p.event = $5::text
 WHERE u.disabled_at IS NULL AND NOT ` + visibility.StudentLike("u") + `
   AND EXISTS (SELECT 1 FROM app.classes c WHERE c.teacher_id = u.id AND c.archived_at IS NULL)
   AND (($2::boolean AND coalesce(p.email, $3::boolean)) OR coalesce(p.in_app, $4::boolean))
 ORDER BY u.id`

func weeklyTeachers(ctx context.Context, q db.Querier, weekly domain.WeeklyOn) ([]domain.WeeklyTeacher, error) {
	defaults := domain.DefaultPreference(domain.EventSummaryWeekly, domain.Staff)
	teachers, err := db.QueryMany(ctx, q, weeklyTeachersQuery,
		[]any{domain.DefaultZone, weekly.Email, defaults.Email, defaults.InApp, string(domain.EventSummaryWeekly)},
		func(rows pgx.Rows) (domain.WeeklyTeacher, error) {
			var t domain.WeeklyTeacher
			err := rows.Scan(&t.ID, &t.Zone, &t.Email, &t.InApp)
			return t, err
		})
	if err != nil {
		return nil, fmt.Errorf("notifications: the weekly summary's teachers: %w", err)
	}
	return teachers, nil
}

var summariseQuery = `
WITH mine AS (
  SELECT c.id, c.name FROM app.classes c WHERE c.teacher_id = $1::uuid AND c.archived_at IS NULL
), sat AS (
  SELECT at.id, at.flagged, k.name AS class_name,
         at.status IN ('submitted','timed_out','graded')
           AND at.submitted_at >= $2::timestamptz AND at.submitted_at < $3::timestamptz AS handed
    FROM mine k
    JOIN app.assignment_classes ac ON ac.class_id = k.id
    JOIN app.assignments a ON a.id = ac.assignment_id AND a.published_at IS NOT NULL
    JOIN app.class_members m ON m.class_id = k.id
    JOIN app.attempts at ON at.assignment_id = a.id AND at.student_id = m.user_id
   WHERE at.status <> 'voided'
     AND ((at.submitted_at >= $2::timestamptz AND at.submitted_at < $3::timestamptz)
          OR (at.started_at >= $2::timestamptz AND at.started_at < $3::timestamptz))
), papers AS (
  SELECT DISTINCT ON (s.id) s.id, s.flagged, s.handed
    FROM sat s ORDER BY s.id
)
SELECT (SELECT count(*) FROM papers p WHERE p.handed)::int,
       (SELECT count(*) FROM papers p
         WHERE p.handed AND EXISTS (SELECT 1 FROM app.attempt_answers ans
                                     WHERE ans.attempt_id = p.id AND ` + answered.AwaitsGrade("ans") + `))::int,
       (SELECT count(*) FROM papers p WHERE p.flagged)::int,
       coalesce((SELECT array_agg(x.name ORDER BY x.name)
                   FROM (SELECT DISTINCT s.class_name AS name FROM sat s
                          WHERE s.handed OR s.flagged ORDER BY 1 LIMIT $4) x), '{}')`

func (r *scheduledRun) WeeklyHeld(ctx context.Context, teacherID string, week domain.Week) (bool, bool, error) {
	var email, inApp bool
	if err := r.tx.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM app.email_outbox WHERE dedupe_key = $2),
		       EXISTS (SELECT 1 FROM app.notifications WHERE user_id = $1::uuid AND dedupe_key = $3)`,
		teacherID, domain.WeeklyEmailKey(teacherID, week), domain.WeeklyKey(week)).Scan(&email, &inApp); err != nil {
		return false, false, fmt.Errorf("notifications: a weekly summary already written: %w", err)
	}
	return email, inApp, nil
}

func (r *scheduledRun) Summarise(ctx context.Context, teacherID string, week domain.Week) (domain.WeeklyFigures, error) {
	s := domain.WeeklyFigures{Week: week}
	if err := r.tx.QueryRow(ctx, summariseQuery, teacherID, week.From, week.To, domain.MaxSummaryClasses).
		Scan(&s.Count, &s.ToGrade, &s.Flagged, &s.ClassNames); err != nil {
		return domain.WeeklyFigures{}, fmt.Errorf("notifications: a weekly summary: %w", err)
	}
	return s, nil
}

const publishWeekly = `
	INSERT INTO app.notifications (user_id, kind, params, target, dedupe_key)
	SELECT $1::uuid, $2::text, $3::text::jsonb, $4::text::jsonb, $5::text
	 WHERE coalesce((SELECT p.in_app FROM app.notification_preferences p
	                  WHERE p.user_id = $1::uuid AND p.event = $6::text), $7::boolean)
	ON CONFLICT (user_id, dedupe_key) DO NOTHING
	RETURNING id::text`

func (r *scheduledRun) Publish(ctx context.Context, n domain.Notice) (bool, error) {
	if err := n.Validate(); err != nil {
		return false, err
	}
	event, governed := n.Kind.Event()
	if !governed {
		return false, fmt.Errorf("notifications: %s has no switch to publish it under", n.Kind)
	}
	params, err := domain.Encode(n.Params)
	if err != nil {
		return false, err
	}
	var target *string
	if n.Target != nil {
		encoded, err := json.Marshal(n.Target)
		if err != nil {
			return false, fmt.Errorf("notifications: target: %w", err)
		}
		target = new(string(encoded))
	}
	var id string
	err = r.tx.QueryRow(ctx, publishWeekly, n.UserID, string(n.Kind), string(params), target, n.DedupeKey,
		string(event), domain.DefaultInApp(event)).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("notifications: publish %s: %w", n.Kind, err)
	}
	return true, nil
}
