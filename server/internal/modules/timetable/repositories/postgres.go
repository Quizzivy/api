// Package repositories keeps the timetable in app.class_session_series and
// app.class_sessions, reads the classes they belong to from app.classes and
// whether a session has attendance from app.attendance_marks, and audits each
// write in its transaction. The bulk loaders read any number of classes in one
// query each.
//
// Every writer of a class's sessions takes the class row's lock first
// (lockClass, only on a class the scope reaches, else ErrNotFound), then the
// series row's (lockSeries), so writes to one class are serialised and an
// archive that committed first is seen. materialise gives an occurrence its
// row under those locks with INSERT … ON CONFLICT (series_id, series_date) DO
// NOTHING, so however many writers race for one occurrence, one row is made;
// attendance (T-R8.6) reuses it.
package repositories

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"quizzivy/internal/modules/timetable/domain"
	"quizzivy/internal/platform/db"
	"quizzivy/internal/shared/access"
	"quizzivy/internal/shared/audit"
	"quizzivy/internal/shared/opt"
)

// Postgres keeps the timetable through dbx.
type Postgres struct {
	db.Repository
}

// NewPostgres keeps the timetable through dbx.
func NewPostgres(dbx db.Context) *Postgres {
	return &Postgres{Repository: db.NewRepository(dbx)}
}

var _ domain.Repository = (*Postgres)(nil)

const (
	dayLayout = "2006-01-02"

	seriesColumns = `s.id::text, s.class_id::text, c.name, s.weekday, to_char(s.start_time, 'HH24:MI'), s.duration_minutes,
		s.room, s.starts_on, s.ends_on, s.time_zone`
	rowColumns = `r.id::text, r.class_id::text, c.name, r.series_id::text, r.series_date, r.starts_at, r.duration_minutes,
		r.room, r.note, r.cancelled_at, EXISTS (SELECT 1 FROM app.attendance_marks m WHERE m.session_id = r.id)`

	reached = `($1::boolean OR c.teacher_id = $2::uuid)`

	forUpdate = `FOR NO KEY UPDATE`
	forShare  = `FOR SHARE`
)

func day(t time.Time) string { return domain.Day(t).Format(dayLayout) }

func optDay(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := day(*t)
	return &s
}

func scanSeries(row pgx.Row) (domain.Series, error) {
	var s domain.Series
	var weekday, duration int16
	var start string
	if err := row.Scan(&s.ID, &s.ClassID, &s.ClassName, &weekday, &start, &duration, &s.Room, &s.StartsOn, &s.EndsOn, &s.TimeZone); err != nil {
		return domain.Series{}, err
	}
	clock, err := domain.ParseClock(start)
	if err != nil {
		return domain.Series{}, err
	}
	s.Weekday, s.Start, s.DurationMinutes = domain.Weekday(weekday), clock, int(duration)
	s.StartsOn = domain.Day(s.StartsOn)
	if s.EndsOn != nil {
		end := domain.Day(*s.EndsOn)
		s.EndsOn = &end
	}
	return s, nil
}

func scanSeriesRows(rows pgx.Rows) (domain.Series, error) { return scanSeries(rows) }

func scanRow(row pgx.Row) (domain.Row, error) {
	var r domain.Row
	var duration int16
	if err := row.Scan(&r.ID, &r.ClassID, &r.ClassName, &r.SeriesID, &r.SeriesDate, &r.StartsAt, &duration,
		&r.Room, &r.Note, &r.CancelledAt, &r.HasAttendance); err != nil {
		return domain.Row{}, err
	}
	r.DurationMinutes = int(duration)
	if r.SeriesDate != nil {
		d := domain.Day(*r.SeriesDate)
		r.SeriesDate = &d
	}
	return r, nil
}

func scanRows(rows pgx.Rows) (domain.Row, error) { return scanRow(rows) }

type class struct {
	name     string
	room     *string
	archived bool
}

func lockClass(ctx context.Context, tx pgx.Tx, scope access.Scope, id, mode string) (class, error) {
	var out class
	err := tx.QueryRow(ctx, `SELECT c.name, c.room, c.archived_at IS NOT NULL FROM app.classes c
		 WHERE c.id = $3::uuid AND `+reached+` `+mode,
		scope.All, opt.String(scope.UserID), id).Scan(&out.name, &out.room, &out.archived)
	if errors.Is(err, pgx.ErrNoRows) {
		return class{}, domain.ErrNotFound
	}
	if err != nil {
		return class{}, fmt.Errorf("timetable: lock class %s: %w", id, err)
	}
	return out, nil
}

func lockSeries(ctx context.Context, tx pgx.Tx, scope access.Scope, id, mode string) (domain.Series, error) {
	var classID string
	err := tx.QueryRow(ctx, `SELECT class_id::text FROM app.class_session_series WHERE id = $1::uuid`, id).Scan(&classID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Series{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Series{}, fmt.Errorf("timetable: read series %s: %w", id, err)
	}
	if _, err := lockClass(ctx, tx, scope, classID, mode); err != nil {
		return domain.Series{}, err
	}
	s, err := scanSeries(tx.QueryRow(ctx, `SELECT `+seriesColumns+`
		  FROM app.class_session_series s JOIN app.classes c ON c.id = s.class_id
		 WHERE s.id = $1::uuid `+mode+` OF s`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Series{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Series{}, fmt.Errorf("timetable: lock series %s: %w", id, err)
	}
	return s, nil
}

func occurrenceRow(ctx context.Context, q db.Querier, seriesID string, date time.Time, mode string) (*domain.Row, error) {
	r, err := scanRow(q.QueryRow(ctx, `SELECT `+rowColumns+`
		  FROM app.class_sessions r JOIN app.classes c ON c.id = r.class_id
		 WHERE r.series_id = $1::uuid AND r.series_date = $2::date `+lockOf(mode, "r"), seriesID, day(date)))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("timetable: read occurrence %s %s: %w", seriesID, day(date), err)
	}
	return &r, nil
}

func lockOf(mode, alias string) string {
	if mode == "" {
		return ""
	}
	return mode + " OF " + alias
}

func materialise(ctx context.Context, tx pgx.Tx, s domain.Series, date time.Time, by domain.Writer) (domain.Row, error) {
	if _, err := tx.Exec(ctx, `
		INSERT INTO app.class_sessions (class_id, series_id, series_date, starts_at, duration_minutes, created_by)
		VALUES ($1::uuid, $2::uuid, $3::date, $4, $5, $6::uuid)
		ON CONFLICT (series_id, series_date) DO NOTHING`,
		s.ClassID, s.ID, day(date), s.StartsAt(date), s.DurationMinutes, opt.String(by.ActorID)); err != nil {
		return domain.Row{}, fmt.Errorf("timetable: materialise %s %s: %w", s.ID, day(date), err)
	}
	r, err := occurrenceRow(ctx, tx, s.ID, date, forUpdate)
	if err != nil {
		return domain.Row{}, err
	}
	if r == nil {
		return domain.Row{}, fmt.Errorf("timetable: materialise %s %s: no row after the insert", s.ID, day(date))
	}
	return *r, nil
}

func record(ctx context.Context, tx pgx.Tx, action, entity, id string, by domain.Writer, facts any) error {
	diff, err := json.Marshal(facts)
	if err != nil {
		return fmt.Errorf("timetable: encode %s diff: %w", action, err)
	}
	return audit.Write(ctx, tx, audit.Entry{
		ActorUserID: opt.String(by.ActorID), Action: action, Entity: entity, EntityID: &id,
		OccurredAt: by.Now, IP: opt.String(by.IP), UserAgent: opt.String(by.UserAgent), Diff: diff,
	})
}

func seriesFacts(s domain.Series) map[string]any {
	return map[string]any{
		"classId": s.ClassID, "weekday": int(s.Weekday), "startTime": s.Start.String(), "durationMinutes": s.DurationMinutes,
		"room": s.Room, "startsOn": day(s.StartsOn), "endsOn": optDay(s.EndsOn), "timeZone": s.TimeZone,
	}
}
