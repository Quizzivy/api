package repositories

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"quizzivy/internal/modules/messaging/domain"
	"quizzivy/internal/platform/db"
	"quizzivy/internal/shared/opt"
	"quizzivy/internal/shared/visibility"
)

// The SQL over a student row u for the picker, with the caller in $1 and
// scope.all in $3: the caller's current classes the student is in, not
// archived, and whether the student is in any current class, not archived,
// that the caller's class scope reaches, or in any at all.
const (
	taughtCurrent = `SELECT c.id, c.name FROM app.class_members cm
	  JOIN app.classes c ON c.id = cm.class_id AND c.archived_at IS NULL AND c.teacher_id = $1::uuid
	  WHERE cm.user_id = u.id`
	currentClassNames = `coalesce((SELECT array_agg(k.name ORDER BY app.immutable_unaccent(lower(k.name)), k.id) FROM (` + taughtCurrent + `) k), '{}')`
	inScopeNow        = `EXISTS (SELECT 1 FROM app.class_members cm
	  JOIN app.classes c ON c.id = cm.class_id AND c.archived_at IS NULL AND ($3::boolean OR c.teacher_id = $1::uuid)
	  WHERE cm.user_id = u.id)`
	inAnyNow = `EXISTS (SELECT 1 FROM app.class_members cm
	  JOIN app.classes c ON c.id = cm.class_id AND c.archived_at IS NULL
	  WHERE cm.user_id = u.id)`
)

// recipientKey is the keyset the picker pages by: the folded name, then the
// id.
func recipientKey(alias string) string {
	return `(` + folded(nameOf(alias)) + `, ` + alias + `.id)`
}

func studentRecipientsSQL() string {
	return `
	SELECT u.id::text, ` + nameOf("u") + `, u.avatar_key, ` + currentClassNames + `, ` + otherClassNames + `,
	       EXISTS (SELECT 1 FROM app.conversations v WHERE v.kind = 'direct' AND v.staff_id = $1::uuid AND v.student_id = u.id),
	       ` + visibility.CanMessage("u", 1) + `
	  FROM app.users u
	 WHERE ` + visibility.CanMessage("u", 1) + `
	   AND ($2::text = '' OR ` + contains(`u.full_name`, 2) + ` OR ` + contains(`coalesce(u.display_name, '')`, 2) + `)
	   AND ($4::uuid IS NULL OR EXISTS (SELECT 1 FROM app.class_members cm WHERE cm.class_id = $4::uuid AND cm.user_id = u.id))
	   AND (NOT $5::boolean OR ` + inScopeNow + `)
	   AND (NOT $6::boolean OR NOT ` + inAnyNow + `)
	   AND ($7::uuid IS NULL OR ` + recipientKey("u") + ` > (SELECT ` + folded(nameOf("k")) + `, k.id FROM app.users k WHERE k.id = $7::uuid))
	 ORDER BY ` + folded(nameOf("u")) + `, u.id
	 LIMIT $8`
}

// StudentRecipients returns at most f.Limit+1 students for the picker after
// the row f.After, by folded name and id: every account the caller may write
// to (visibility.CanMessage), matched on the name accent-insensitively and
// kept by f's one filter. A class f's scope does not reach answers
// ErrNotFound, as a missing one does. A cursor whose account is gone ends
// the list.
func (p *Postgres) StudentRecipients(ctx context.Context, f domain.RecipientFilter) ([]domain.Recipient, error) {
	if f.ClassID != "" {
		reached, err := db.Exists(ctx, p, `SELECT 1 FROM app.classes c WHERE c.id = $1::uuid AND ($2::boolean OR c.teacher_id = $3::uuid)`,
			f.ClassID, f.All, opt.String(f.ActorID))
		if err != nil {
			return nil, fmt.Errorf("messaging: recipients class: %w", err)
		}
		if !reached {
			return nil, domain.ErrNotFound
		}
	}
	rows, err := db.QueryMany(ctx, p, studentRecipientsSQL(),
		[]any{opt.String(f.ActorID), db.EscapeLike(f.Query), f.All, opt.String(f.ClassID), f.Mine, f.InNoClass, opt.String(f.After), f.Limit + 1},
		func(rows pgx.Rows) (domain.Recipient, error) {
			var r domain.Recipient
			err := rows.Scan(&r.ID, &r.Name, &r.AvatarKey, &r.ClassNames, &r.OtherClassNames, &r.ThreadExists, &r.CanPost)
			if r.ClassNames == nil {
				r.ClassNames = []string{}
			}
			if r.OtherClassNames == nil {
				r.OtherClassNames = []string{}
			}
			return r, err
		})
	if err != nil {
		return nil, fmt.Errorf("messaging: recipients: %w", err)
	}
	return rows, nil
}
