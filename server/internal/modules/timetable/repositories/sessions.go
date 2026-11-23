package repositories

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"quizzivy/internal/modules/timetable/domain"
	"quizzivy/internal/platform/db"
	"quizzivy/internal/shared/access"
	"quizzivy/internal/shared/opt"
)

const (
	entitySession = "class_session"
	entitySeries  = "class_session_series"
)

func windowArgs(w domain.Window) []any {
	return []any{
		w.Scope.All, opt.String(w.Scope.UserID), w.ClassIDs,
		day(w.From.UTC().AddDate(0, 0, -1)), day(w.To.UTC().AddDate(0, 0, 1)), w.From, w.To,
	}
}

const windowRows = `
	  FROM app.class_sessions r JOIN app.classes c ON c.id = r.class_id
	 WHERE ` + reached + `
	   AND ($3::uuid[] IS NULL OR r.class_id = ANY($3::uuid[]))
	   AND ((r.starts_at >= $6 AND r.starts_at < $7) OR r.series_date BETWEEN $4::date AND $5::date)`

// Rows returns the rows of w's classes that start in w or name an occurrence
// dated within a day of it, in one query.
func (p *Postgres) Rows(ctx context.Context, w domain.Window) ([]domain.Row, error) {
	out, err := db.QueryMany(ctx, p, `SELECT `+rowColumns+windowRows+` ORDER BY r.starts_at, r.id`, windowArgs(w), scanRows)
	if err != nil {
		return nil, fmt.Errorf("timetable: load sessions: %w", err)
	}
	return out, nil
}

// Series returns the series of w's classes that may meet in w, and every
// series a row Rows returns names, in one query.
func (p *Postgres) Series(ctx context.Context, w domain.Window) ([]domain.Series, error) {
	out, err := db.QueryMany(ctx, p, `SELECT `+seriesColumns+`
		  FROM app.class_session_series s JOIN app.classes c ON c.id = s.class_id
		 WHERE `+reached+`
		   AND ($3::uuid[] IS NULL OR s.class_id = ANY($3::uuid[]))
		   AND ((s.starts_on <= $5::date AND (s.ends_on IS NULL OR s.ends_on >= $4::date))
		        OR s.id IN (SELECT r.series_id `+windowRows+`))
		 ORDER BY s.starts_on, s.id`, windowArgs(w), scanSeriesRows)
	if err != nil {
		return nil, fmt.Errorf("timetable: load series: %w", err)
	}
	return out, nil
}

// Find resolves id under scope, reading without a lock.
func (p *Postgres) Find(ctx context.Context, scope access.Scope, id domain.SessionID) (domain.Found, error) {
	if !id.Occurrence() {
		r, err := scanRow(p.QueryRow(ctx, `SELECT `+rowColumns+`
			  FROM app.class_sessions r JOIN app.classes c ON c.id = r.class_id
			 WHERE r.id = $3::uuid AND r.series_id IS NULL AND `+reached, scope.All, opt.String(scope.UserID), id.OneOff))
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Found{}, domain.ErrNotFound
		}
		if err != nil {
			return domain.Found{}, fmt.Errorf("timetable: read session %s: %w", id, err)
		}
		return domain.Found{ID: id, Row: &r}, nil
	}
	s, err := scanSeries(p.QueryRow(ctx, `SELECT `+seriesColumns+`
		  FROM app.class_session_series s JOIN app.classes c ON c.id = s.class_id
		 WHERE s.id = $3::uuid AND `+reached, scope.All, opt.String(scope.UserID), id.SeriesID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Found{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Found{}, fmt.Errorf("timetable: read series %s: %w", id.SeriesID, err)
	}
	r, err := occurrenceRow(ctx, p, s.ID, id.Date, "")
	if err != nil {
		return domain.Found{}, err
	}
	return occurrenceOf(id, s, r)
}

func occurrenceOf(id domain.SessionID, s domain.Series, r *domain.Row) (domain.Found, error) {
	if r == nil && !s.Meets(id.Date) {
		return domain.Found{}, domain.ErrNotFound
	}
	return domain.Found{ID: id, Series: &s, Row: r}, nil
}

func lockSession(ctx context.Context, tx pgx.Tx, scope access.Scope, id domain.SessionID) (domain.Found, error) {
	if id.Occurrence() {
		s, err := lockSeries(ctx, tx, scope, id.SeriesID, forUpdate)
		if err != nil {
			return domain.Found{}, err
		}
		r, err := occurrenceRow(ctx, tx, s.ID, id.Date, forUpdate)
		if err != nil {
			return domain.Found{}, err
		}
		return occurrenceOf(id, s, r)
	}
	var classID string
	err := tx.QueryRow(ctx, `SELECT class_id::text FROM app.class_sessions WHERE id = $1::uuid AND series_id IS NULL`, id.OneOff).Scan(&classID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Found{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Found{}, fmt.Errorf("timetable: read session %s: %w", id, err)
	}
	if _, err := lockClass(ctx, tx, scope, classID, forUpdate); err != nil {
		return domain.Found{}, err
	}
	r, err := scanRow(tx.QueryRow(ctx, `SELECT `+rowColumns+`
		  FROM app.class_sessions r JOIN app.classes c ON c.id = r.class_id
		 WHERE r.id = $1::uuid AND r.series_id IS NULL FOR NO KEY UPDATE OF r`, id.OneOff))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Found{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Found{}, fmt.Errorf("timetable: lock session %s: %w", id, err)
	}
	return domain.Found{ID: id, Row: &r}, nil
}

// Create stores the series or the one-off build makes under the class's lock.
func (p *Postgres) Create(ctx context.Context, scope access.Scope, classID string, build func(*string) (*domain.Series, *domain.NewOneOff, error), by domain.Writer) (domain.Created, error) {
	var out domain.Created
	err := p.InTx(ctx, "timetable: create session", func(tx pgx.Tx) error {
		c, err := lockClass(ctx, tx, scope, classID, forUpdate)
		if err != nil {
			return err
		}
		if c.archived {
			return domain.ErrClassArchived
		}
		series, oneOff, err := build(c.room)
		if err != nil {
			return err
		}
		if series != nil {
			series.ClassID, series.ClassName = classID, c.name
			s, err := insertSeries(ctx, tx, *series, by)
			if err != nil {
				return err
			}
			out.Series = &s
			return record(ctx, tx, "session.created", entitySeries, s.ID, by, seriesFacts(s))
		}
		r := domain.Row{ClassID: classID, ClassName: c.name, StartsAt: oneOff.StartsAt, DurationMinutes: oneOff.DurationMinutes, Room: oneOff.Room}
		if err := tx.QueryRow(ctx, `
			INSERT INTO app.class_sessions (class_id, starts_at, duration_minutes, room, created_by)
			VALUES ($1::uuid, $2, $3, $4, $5::uuid) RETURNING id::text`,
			classID, r.StartsAt, r.DurationMinutes, r.Room, opt.String(by.ActorID)).Scan(&r.ID); err != nil {
			return fmt.Errorf("timetable: insert session: %w", err)
		}
		out.Row = &r
		return record(ctx, tx, "session.created", entitySession, r.ID, by, oneOffFactsOf(r))
	})
	if err != nil {
		return domain.Created{}, err
	}
	return out, nil
}

func insertSeries(ctx context.Context, tx pgx.Tx, s domain.Series, by domain.Writer) (domain.Series, error) {
	if err := tx.QueryRow(ctx, `
		INSERT INTO app.class_session_series (class_id, weekday, start_time, duration_minutes, room, starts_on, ends_on, time_zone, created_by)
		VALUES ($1::uuid, $2, $3::time, $4, $5, $6::date, $7::date, $8, $9::uuid) RETURNING id::text`,
		s.ClassID, int(s.Weekday), s.Start.String(), s.DurationMinutes, s.Room, day(s.StartsOn), optDay(s.EndsOn), s.TimeZone,
		opt.String(by.ActorID)).Scan(&s.ID); err != nil {
		return domain.Series{}, fmt.Errorf("timetable: insert series: %w", err)
	}
	return s, nil
}

// EditSession writes the edit change makes of the session under its class's
// lock, and its series' for an occurrence.
func (p *Postgres) EditSession(ctx context.Context, scope access.Scope, id domain.SessionID, change func(domain.Found) (domain.Edit, error), by domain.Writer) (domain.Found, error) {
	var out domain.Found
	err := p.InTx(ctx, "timetable: edit session", func(tx pgx.Tx) error {
		f, err := lockSession(ctx, tx, scope, id)
		if err != nil {
			return err
		}
		edit, err := change(f)
		if err != nil {
			return err
		}
		out = f
		if !edit.Changed || (!edit.Store && f.Row == nil) {
			return nil
		}
		rowID, err := writeEdit(ctx, tx, f, edit, by)
		if err != nil {
			return err
		}
		out.Row = editedRow(f, edit, rowID)
		action := "session.updated"
		if edit.Cancelled {
			action = "session.cancelled"
		}
		return record(ctx, tx, action, entitySession, rowID, by, editFactsOf(f, edit))
	})
	if err != nil {
		return domain.Found{}, err
	}
	return out, nil
}

func editedRow(f domain.Found, edit domain.Edit, rowID string) *domain.Row {
	if !edit.Store {
		return nil
	}
	next := domain.Row{ID: rowID, HasAttendance: f.Row != nil && f.Row.HasAttendance}
	if f.Series != nil {
		date := f.ID.Date
		next.ClassID, next.ClassName, next.SeriesID, next.SeriesDate = f.Series.ClassID, f.Series.ClassName, &f.Series.ID, &date
	} else {
		next.ClassID, next.ClassName = f.Row.ClassID, f.Row.ClassName
	}
	e := edit.Fields
	next.StartsAt, next.DurationMinutes, next.Room, next.Note, next.CancelledAt = e.StartsAt, e.DurationMinutes, e.Room, e.Note, e.CancelledAt
	return &next
}

func writeEdit(ctx context.Context, tx pgx.Tx, f domain.Found, edit domain.Edit, by domain.Writer) (string, error) {
	e := edit.Fields
	switch {
	case !edit.Store:
		if _, err := tx.Exec(ctx, `DELETE FROM app.class_sessions WHERE id = $1::uuid`, f.Row.ID); err != nil {
			return "", fmt.Errorf("timetable: delete the row of %s: %w", f.ID, err)
		}
		return f.Row.ID, nil
	case f.Row != nil:
		if _, err := tx.Exec(ctx, `
			UPDATE app.class_sessions SET starts_at = $2, duration_minutes = $3, room = $4, note = $5, cancelled_at = $6
			 WHERE id = $1::uuid`, f.Row.ID, e.StartsAt, e.DurationMinutes, e.Room, e.Note, e.CancelledAt); err != nil {
			return "", fmt.Errorf("timetable: update session %s: %w", f.ID, err)
		}
		return f.Row.ID, nil
	default:
		r, err := materialise(ctx, tx, *f.Series, f.ID.Date, by)
		if err != nil {
			return "", err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE app.class_sessions SET starts_at = $2, duration_minutes = $3, room = $4, note = $5, cancelled_at = $6
			 WHERE id = $1::uuid`, r.ID, e.StartsAt, e.DurationMinutes, e.Room, e.Note, e.CancelledAt); err != nil {
			return "", fmt.Errorf("timetable: update session %s: %w", f.ID, err)
		}
		return r.ID, nil
	}
}

type oneOffFacts struct {
	ClassID         string    `json:"classId"`
	StartsAt        time.Time `json:"startsAt"`
	DurationMinutes int       `json:"durationMinutes"`
	Room            *string   `json:"room"`
	Cancelled       bool      `json:"cancelled"`
}

func oneOffFactsOf(r domain.Row) oneOffFacts {
	return oneOffFacts{ClassID: r.ClassID, StartsAt: r.StartsAt.UTC(), DurationMinutes: r.DurationMinutes, Room: r.Room, Cancelled: r.CancelledAt != nil}
}

type editFacts struct {
	SessionID string         `json:"sessionId"`
	ClassID   string         `json:"classId"`
	Fields    []string       `json:"fields"`
	From      map[string]any `json:"from"`
	To        map[string]any `json:"to"`
}

func editFactsOf(f domain.Found, edit domain.Edit) editFacts {
	from, classID := f.Fields(), ""
	if f.Series != nil {
		classID = f.Series.ClassID
	} else {
		classID = f.Row.ClassID
	}
	out := editFacts{SessionID: f.ID.String(), ClassID: classID, Fields: edit.Names, From: map[string]any{}, To: map[string]any{}}
	for _, name := range edit.Names {
		switch name {
		case "startsAt":
			out.From[name], out.To[name] = from.StartsAt.UTC(), edit.Fields.StartsAt.UTC()
		case "durationMinutes":
			out.From[name], out.To[name] = from.DurationMinutes, edit.Fields.DurationMinutes
		case "room":
			out.From[name], out.To[name] = from.Room, edit.Fields.Room
		case "cancelled":
			out.From[name], out.To[name] = from.CancelledAt != nil, edit.Fields.CancelledAt != nil
		}
	}
	return out
}

// DeleteOneOff removes a one-off under its class's lock.
func (p *Postgres) DeleteOneOff(ctx context.Context, scope access.Scope, id domain.SessionID, by domain.Writer) error {
	return p.InTx(ctx, "timetable: delete session", func(tx pgx.Tx) error {
		f, err := lockSession(ctx, tx, scope, id)
		if err != nil {
			return err
		}
		if f.Series != nil {
			return domain.ErrInSeries
		}
		if f.Row.HasAttendance {
			return domain.ErrHasAttendance
		}
		if _, err := tx.Exec(ctx, `DELETE FROM app.class_sessions WHERE id = $1::uuid`, f.Row.ID); err != nil {
			if _, restricted := db.ForeignKeyViolation(err); restricted {
				return domain.ErrHasAttendance
			}
			return fmt.Errorf("timetable: delete session %s: %w", id, err)
		}
		return record(ctx, tx, "session.deleted", entitySession, f.Row.ID, by, oneOffFactsOf(*f.Row))
	})
}
