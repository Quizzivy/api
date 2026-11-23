package repositories

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"quizzivy/internal/modules/timetable/domain"
	"quizzivy/internal/shared/access"
)

const withoutAttendance = `NOT EXISTS (SELECT 1 FROM app.attendance_marks m WHERE m.session_id = r.id)`

func attendedDates(ctx context.Context, tx pgx.Tx, seriesID string) (map[string]bool, error) {
	rows, err := tx.Query(ctx, `SELECT r.series_date FROM app.class_sessions r
		 WHERE r.series_id = $1::uuid AND NOT `+withoutAttendance, seriesID)
	if err != nil {
		return nil, fmt.Errorf("timetable: the attended dates of %s: %w", seriesID, err)
	}
	dates, err := pgx.CollectRows(rows, pgx.RowTo[time.Time])
	if err != nil {
		return nil, fmt.Errorf("timetable: the attended dates of %s: %w", seriesID, err)
	}
	out := make(map[string]bool, len(dates))
	for _, d := range dates {
		out[day(d)] = true
	}
	return out, nil
}

func dropRows(ctx context.Context, tx pgx.Tx, seriesID, op string, from time.Time) (int, error) {
	tag, err := tx.Exec(ctx, `DELETE FROM app.class_sessions r
		 WHERE r.series_id = $1::uuid AND r.series_date `+op+` $2::date AND `+withoutAttendance, seriesID, day(from))
	if err != nil {
		return 0, fmt.Errorf("timetable: drop the rows of %s: %w", seriesID, err)
	}
	return int(tag.RowsAffected()), nil
}

// EditSeries stores the split plan makes of the series under its class's and
// its own lock.
func (p *Postgres) EditSeries(ctx context.Context, scope access.Scope, seriesID string, plan func(domain.Series, func(time.Time) bool) (domain.Split, error), by domain.Writer) (domain.SeriesEdit, error) {
	var out domain.SeriesEdit
	err := p.InTx(ctx, "timetable: edit series", func(tx pgx.Tx) error {
		s, err := lockSeries(ctx, tx, scope, seriesID, forUpdate)
		if err != nil {
			return err
		}
		attended, err := attendedDates(ctx, tx, s.ID)
		if err != nil {
			return err
		}
		split, err := plan(s, func(d time.Time) bool { return attended[day(d)] })
		if err != nil {
			return err
		}
		out.Series = s
		if len(split.Fields) == 0 {
			return nil
		}
		if out.Series, err = storeSplit(ctx, tx, split, by); err != nil {
			return err
		}
		if out.Dropped, err = dropRows(ctx, tx, s.ID, ">=", split.From); err != nil {
			return err
		}
		return record(ctx, tx, "session_series.updated", entitySeries, s.ID, by, splitFactsOf(split, out))
	})
	if err != nil {
		return domain.SeriesEdit{}, err
	}
	return out, nil
}

func storeSplit(ctx context.Context, tx pgx.Tx, split domain.Split, by domain.Writer) (domain.Series, error) {
	old, next := split.Old, split.Next
	if split.InPlace {
		if _, err := tx.Exec(ctx, `
			UPDATE app.class_session_series SET weekday = $2, start_time = $3::time, duration_minutes = $4, room = $5
			 WHERE id = $1::uuid`, old.ID, int(next.Weekday), next.Start.String(), next.DurationMinutes, next.Room); err != nil {
			return domain.Series{}, fmt.Errorf("timetable: update series %s: %w", old.ID, err)
		}
		return next, nil
	}
	if _, err := tx.Exec(ctx, `UPDATE app.class_session_series SET ends_on = $2::date WHERE id = $1::uuid`, old.ID, optDay(old.EndsOn)); err != nil {
		return domain.Series{}, fmt.Errorf("timetable: end series %s: %w", old.ID, err)
	}
	return insertSeries(ctx, tx, next, by)
}

type splitFacts struct {
	SeriesID       string         `json:"seriesId"`
	NewSeriesID    string         `json:"newSeriesId"`
	ClassID        string         `json:"classId"`
	FromDate       string         `json:"fromDate"`
	Fields         []string       `json:"fields"`
	From           map[string]any `json:"from"`
	To             map[string]any `json:"to"`
	DroppedChanges int            `json:"droppedChanges"`
}

func splitFactsOf(split domain.Split, edit domain.SeriesEdit) splitFacts {
	out := splitFacts{
		SeriesID: split.Old.ID, NewSeriesID: edit.Series.ID, ClassID: split.Old.ClassID, FromDate: day(split.From),
		Fields: split.Fields, From: map[string]any{}, To: map[string]any{}, DroppedChanges: edit.Dropped,
	}
	before, after := seriesFacts(split.Old), seriesFacts(edit.Series)
	for _, f := range split.Fields {
		out.From[f], out.To[f] = before[f], after[f]
	}
	return out
}

type endFacts struct {
	SeriesID       string  `json:"seriesId"`
	ClassID        string  `json:"classId"`
	LastDate       string  `json:"lastDate"`
	EndsOn         *string `json:"endsOn"`
	Removed        bool    `json:"removed"`
	DroppedChanges int     `json:"droppedChanges"`
}

// EndSeries stores the ending plan makes of the series under its class's and
// its own lock.
func (p *Postgres) EndSeries(ctx context.Context, scope access.Scope, seriesID string, plan func(domain.Series) (domain.Ending, error), by domain.Writer) (*domain.Series, error) {
	var out *domain.Series
	err := p.InTx(ctx, "timetable: end series", func(tx pgx.Tx) error {
		s, err := lockSeries(ctx, tx, scope, seriesID, forUpdate)
		if err != nil {
			return err
		}
		end, err := plan(s)
		if err != nil {
			return err
		}
		if err := refuseAttendanceAfter(ctx, tx, s.ID, end.LastDate); err != nil {
			return err
		}
		out, err = storeEnding(ctx, tx, s, end, by)
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func storeEnding(ctx context.Context, tx pgx.Tx, s domain.Series, end domain.Ending, by domain.Writer) (*domain.Series, error) {
	facts := endFacts{SeriesID: s.ID, ClassID: s.ClassID, LastDate: day(end.LastDate), EndsOn: optDay(s.EndsOn), Removed: end.Remove}
	var err error
	if end.Remove {
		if facts.DroppedChanges, err = removeSeries(ctx, tx, s); err != nil {
			return nil, err
		}
		return nil, record(ctx, tx, "session_series.ended", entitySeries, s.ID, by, facts)
	}
	if facts.DroppedChanges, err = dropRows(ctx, tx, s.ID, ">", end.LastDate); err != nil {
		return nil, err
	}
	ended := s
	ended.EndsOn = &end.LastDate
	if facts.DroppedChanges == 0 && s.EndsOn != nil && s.EndsOn.Equal(end.LastDate) {
		return &ended, nil
	}
	if _, err := tx.Exec(ctx, `UPDATE app.class_session_series SET ends_on = $2::date WHERE id = $1::uuid`, s.ID, day(end.LastDate)); err != nil {
		return nil, fmt.Errorf("timetable: end series %s: %w", s.ID, err)
	}
	return &ended, record(ctx, tx, "session_series.ended", entitySeries, s.ID, by, facts)
}

func refuseAttendanceAfter(ctx context.Context, tx pgx.Tx, seriesID string, last time.Time) error {
	attended, err := attendedDates(ctx, tx, seriesID)
	if err != nil {
		return err
	}
	for d := range attended {
		if d > day(last) {
			return domain.ErrHasAttendance
		}
	}
	return nil
}

func removeSeries(ctx context.Context, tx pgx.Tx, s domain.Series) (int, error) {
	dropped, err := dropRows(ctx, tx, s.ID, ">=", s.StartsOn)
	if err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM app.class_session_series WHERE id = $1::uuid`, s.ID); err != nil {
		return 0, fmt.Errorf("timetable: remove series %s: %w", s.ID, err)
	}
	return dropped, nil
}
