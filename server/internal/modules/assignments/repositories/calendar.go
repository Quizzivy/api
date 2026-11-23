package repositories

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"quizzivy/internal/modules/assignments/domain"
	"quizzivy/internal/platform/db"
	"quizzivy/internal/shared/opt"
	"quizzivy/internal/shared/schedule"
	"quizzivy/internal/shared/visibility"
)

// calendarWindows reads each published assignment the scope reaches ($1
// scope.all, $2 the reader) whose own window opens or closes in [$4, $5),
// only those targeting class $3 when it is not NULL, with the names of its
// target classes the reader teaches (all of them under scope.all).
var calendarWindows = `
	SELECT a.id::text, t.title, a.opens_at, ` + schedule.CloseOf("") + `,
	       coalesce((SELECT array_agg(c.name ORDER BY c.name, c.id)
	                   FROM app.assignment_classes ac
	                   JOIN app.classes c ON c.id = ac.class_id
	                  WHERE ac.assignment_id = a.id
	                    AND ($1::boolean OR c.id IN ` + visibility.TaughtClassIDs(2) + `)), '{}'::text[])
	  FROM app.assignments a
	  JOIN app.tests t ON t.id = a.test_id
	 WHERE a.published_at IS NOT NULL
	   AND ($1::boolean OR a.id IN ` + visibility.AssignmentIDs(2) + `)
	   AND ($3::uuid IS NULL OR EXISTS (SELECT 1 FROM app.assignment_classes ac
	                                     WHERE ac.assignment_id = a.id AND ac.class_id = $3::uuid))
	   AND ((a.opens_at >= $4 AND a.opens_at < $5)
	     OR (` + schedule.CloseOf("") + ` >= $4 AND ` + schedule.CloseOf("") + ` < $5))
	 ORDER BY a.opens_at, a.id`

// CalendarWindows reads the published assignments the input's scope reaches
// whose own window opens or closes in its range, in one query.
func (s *Postgres) CalendarWindows(ctx context.Context, in domain.CalendarInput) ([]domain.CalendarWindow, error) {
	out, err := db.QueryMany(ctx, s, calendarWindows,
		[]any{in.Scope.All, opt.String(in.Scope.UserID), opt.String(in.ClassID), in.From, in.To},
		func(rows pgx.Rows) (domain.CalendarWindow, error) {
			var w domain.CalendarWindow
			err := rows.Scan(&w.ID, &w.Title, &w.OpensAt, &w.ClosesAt, &w.ClassNames)
			return w, err
		})
	if err != nil {
		return nil, fmt.Errorf("assignments: read calendar windows: %w", err)
	}
	return out, nil
}
