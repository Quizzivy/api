package repositories

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"

	"quizzivy/internal/modules/notifications/domain"
	"quizzivy/internal/platform/db"
	"quizzivy/internal/platform/email"
	"quizzivy/internal/shared/schedule"
)

// SchedulerLockKey is the notification scheduler's key in the 73819
// advisory-lock namespace (20-data-model.md §28): one run at a time across
// the API's machines.
const SchedulerLockKey = 42

// Schedule is the notification scheduler's store (T-R7.14,
// domain.Scheduled). Its reads cross into the assignments, attempts and
// classes tables, as Due's do, and judge them as those modules do
// (shared/schedule, shared/visibility); its emails go through Mail.
type Schedule struct {
	db.Repository
	outbox *email.Outbox
	logger *slog.Logger
}

// NewSchedule is the scheduler's store over dbx, writing into outbox. A zone
// a teacher's profile holds that this host cannot load is logged on logger
// and that teacher left out.
func NewSchedule(dbx db.Context, outbox *email.Outbox, logger *slog.Logger) *Schedule {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &Schedule{Repository: db.NewRepository(dbx), outbox: outbox, logger: logger}
}

var upcomingQuery = `
WITH closes AS (
  SELECT ` + schedule.CloseOf("") + ` AS closes, a.review_release = 'after_close' AND a.review_show_score AS releases, true AS own
    FROM app.assignments a
   WHERE a.published_at IS NOT NULL AND ` + schedule.CloseOf("") + ` > $1::timestamptz
  UNION ALL
  SELECT o.closes_at, a.review_release = 'after_close' AND a.review_show_score, false
    FROM app.assignment_student_overrides o
    JOIN app.assignments a ON a.id = o.assignment_id
   WHERE a.published_at IS NOT NULL AND o.closes_at > ` + schedule.CloseOf("") + ` AND o.closes_at > $1::timestamptz
)
SELECT min(m.at) FILTER (WHERE m.what = 'due'),
       min(m.at) FILTER (WHERE m.what = 'closing'),
       min(m.at) FILTER (WHERE m.what = 'release')
  FROM closes c
 CROSS JOIN LATERAL (VALUES ('due', c.closes - interval '24 hours'),
                            ('due', c.closes - interval '1 hour'),
                            ('closing', CASE WHEN c.own THEN c.closes - interval '1 hour' END),
                            ('release', CASE WHEN c.releases THEN c.closes END)) AS m(what, at)
 WHERE m.at > $1::timestamptz`

// Upcoming reads the earliest moment after now of each kind the scheduler
// sends, over every published assignment and every override that moves a
// close later, and the zones of the teachers WeeklyTeachers would answer.
func (s *Schedule) Upcoming(ctx context.Context, now time.Time, weekly domain.WeeklyOn) (domain.Upcoming, error) {
	var due, closing, release *time.Time
	if err := s.QueryRow(ctx, upcomingQuery, now).Scan(&due, &closing, &release); err != nil {
		return domain.Upcoming{}, fmt.Errorf("notifications: the scheduler's next moments: %w", err)
	}
	teachers, err := weeklyTeachers(ctx, s, weekly)
	if err != nil {
		return domain.Upcoming{}, err
	}
	u := domain.Upcoming{DueSoon: deref(due), Closing: deref(closing), Release: deref(release)}
	seen := map[string]bool{}
	for _, teacher := range teachers {
		if seen[teacher.Zone] {
			continue
		}
		seen[teacher.Zone] = true
		if zone, ok := s.zone(teacher.Zone); ok {
			u.Zones = append(u.Zones, zone)
		}
	}
	return u, nil
}

func (s *Schedule) zone(name string) (*time.Location, bool) {
	if zone, ok := domain.Zone(name); ok {
		return zone, true
	}
	s.logger.Warn("a teacher's time zone cannot be loaded; their weekly summary is skipped", "code", "WEEKLY_SUMMARY_ZONE_INVALID")
	return nil, false
}

func deref(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}

// Locked runs fn in a transaction that first takes
// pg_try_advisory_xact_lock(73819, SchedulerLockKey), and answers false
// without running it when another run holds the lock. Once fn's writes
// commit, the outbox is woken if it wrote an email.
func (s *Schedule) Locked(ctx context.Context, fn func(domain.ScheduledRun) error) (bool, error) {
	var locked bool
	run := &scheduledRun{store: s}
	err := s.InTx(ctx, "notifications: scheduled run", func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(73819, $1)`, SchedulerLockKey).Scan(&locked); err != nil {
			return fmt.Errorf("notifications: the scheduler's lock: %w", err)
		}
		if !locked {
			return nil
		}
		run.tx = tx
		run.mail = NewMail(db.NewContext(tx), s.outbox)
		return fn(run)
	})
	if err != nil {
		return false, err
	}
	if run.wrote && s.outbox != nil {
		s.outbox.Notify()
	}
	return locked, nil
}

type scheduledRun struct {
	store *Schedule
	tx    pgx.Tx
	mail  *Mail
	wrote bool
}

func (r *scheduledRun) Enqueue(ctx context.Context, e domain.Email) (bool, error) {
	if r.mail.outbox == nil {
		return false, errors.New("notifications: a scheduler without an outbox cannot enqueue")
	}
	params, err := paramsOf(e)
	if err != nil {
		return false, err
	}
	wrote, err := r.mail.write(ctx, r.tx, e, email.Mail{ToUserID: e.UserID, Params: params, DedupeKey: e.DedupeKey, DeliverAfter: e.DeliverAfter})
	if err != nil {
		return false, err
	}
	r.wrote = r.wrote || wrote
	return wrote, nil
}

func (r *scheduledRun) WeeklyTeachers(ctx context.Context, weekly domain.WeeklyOn) ([]domain.WeeklyTeacher, error) {
	return weeklyTeachers(ctx, r.tx, weekly)
}

func held(ctx context.Context, q db.Querier, keys []string) (map[string]bool, error) {
	out := map[string]bool{}
	if len(keys) == 0 {
		return out, nil
	}
	rows, err := db.QueryMany(ctx, q, `SELECT dedupe_key FROM app.email_outbox WHERE dedupe_key = ANY($1::text[])`, []any{keys},
		func(rows pgx.Rows) (string, error) {
			var key string
			err := rows.Scan(&key)
			return key, err
		})
	if err != nil {
		return nil, fmt.Errorf("notifications: the emails already queued: %w", err)
	}
	for _, key := range rows {
		out[key] = true
	}
	return out, nil
}
