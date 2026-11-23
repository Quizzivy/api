package repositories

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"quizzivy/internal/platform/db"
)

// MemberClasses returns the classes studentID is a member of now, archived
// ones left out, in one query. A student who left a class has no member row,
// so the class is not among them.
func (p *Postgres) MemberClasses(ctx context.Context, studentID string) ([]string, error) {
	out, err := db.QueryMany(ctx, p, `SELECT m.class_id::text
		  FROM app.class_members m JOIN app.classes c ON c.id = m.class_id
		 WHERE m.user_id = $1::uuid AND c.archived_at IS NULL
		 ORDER BY m.class_id`, []any{studentID}, func(rows pgx.Rows) (string, error) {
		var id string
		return id, rows.Scan(&id)
	})
	if err != nil {
		return nil, fmt.Errorf("timetable: the classes of %s: %w", studentID, err)
	}
	return out, nil
}
