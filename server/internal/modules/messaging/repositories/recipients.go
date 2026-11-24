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

// afterRow is the keyset condition that the row u comes after the cursor's
// row, the account in placeholder $n, read again only while it is one of
// the audience's own rows (eligible, the SQL over k): a cursor naming any
// other account, forged or one that has left the audience since, makes the
// subquery empty and so ends the list, and never places an account the
// picker does not list in the order of names.
func afterRow(n int, eligible func(alias string) string) string {
	return fmt.Sprintf(`($%[1]d::uuid IS NULL OR %[2]s > (SELECT %[3]s, k.id FROM app.users k WHERE k.id = $%[1]d::uuid AND %[4]s))`,
		n, recipientKey("u"), folded(nameOf("k")), eligible("k"))
}

func namedLike(param int) string {
	return fmt.Sprintf(`($%[1]d::text = '' OR %[2]s OR %[3]s)`, param, contains(`u.full_name`, param), contains(`coalesce(u.display_name, '')`, param))
}

func studentRecipientsSQL() string {
	return `
	SELECT u.id::text, ` + nameOf("u") + `, u.avatar_key, ` + currentClassNames + `, ` + otherClassNames + `,
	       EXISTS (SELECT 1 FROM app.conversations v WHERE v.kind = 'direct' AND v.staff_id = $1::uuid AND v.student_id = u.id),
	       ` + visibility.CanMessage("u", 1) + `
	  FROM app.users u
	 WHERE ` + visibility.CanMessage("u", 1) + `
	   AND ` + namedLike(2) + `
	   AND ($4::uuid IS NULL OR EXISTS (SELECT 1 FROM app.class_members cm WHERE cm.class_id = $4::uuid AND cm.user_id = u.id))
	   AND (NOT $5::boolean OR ` + inScopeNow + `)
	   AND (NOT $6::boolean OR NOT ` + inAnyNow + `)
	   AND ` + afterRow(7, func(k string) string { return visibility.CanMessage(k, 1) }) + `
	 ORDER BY ` + folded(nameOf("u")) + `, u.id
	 LIMIT $8`
}

func colleagueRecipientsSQL() string {
	return `
	SELECT u.id::text, ` + nameOf("u") + `, u.avatar_key, '{}'::text[], '{}'::text[], r.name, r.builtin_key,
	       EXISTS (SELECT 1 FROM app.conversations v WHERE ` + pairOf("$1::uuid", "u.id") + `),
	       ` + canColleague("u") + `
	  FROM app.users u
	  JOIN app.roles r ON r.id = u.role_id
	 WHERE ` + canColleague("u") + `
	   AND ` + namedLike(2) + `
	   AND ` + afterRow(3, canColleague) + `
	 ORDER BY ` + folded(nameOf("u")) + `, u.id
	 LIMIT $4`
}

// StudentRecipients returns at most f.Limit+1 students for the picker after
// the row f.After, by folded name and id: every account the caller may write
// to (visibility.CanMessage), matched on the name accent-insensitively and
// kept by f's one filter. A class f's scope does not reach answers
// ErrNotFound, as a missing one does. A cursor whose account is gone, or is
// no student the caller may write to, ends the list.
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
			return withNames(r), err
		})
	if err != nil {
		return nil, fmt.Errorf("messaging: recipients: %w", err)
	}
	return rows, nil
}

// ColleagueRecipients returns at most f.Limit+1 colleagues for the picker
// after the row f.After, by folded name and id (domain.Repository): every
// enabled account that opens the teacher workspace and is not a student,
// anonymised or the caller, matched on the name accent-insensitively, with
// the role's stored name and built-in key and whether the caller's thread
// with them exists. A cursor whose account is gone, or is no colleague the
// caller may write to, ends the list.
func (p *Postgres) ColleagueRecipients(ctx context.Context, f domain.RecipientFilter) ([]domain.Recipient, error) {
	rows, err := db.QueryMany(ctx, p, colleagueRecipientsSQL(),
		[]any{opt.String(f.ActorID), db.EscapeLike(f.Query), opt.String(f.After), f.Limit + 1},
		func(rows pgx.Rows) (domain.Recipient, error) {
			var r domain.Recipient
			err := rows.Scan(&r.ID, &r.Name, &r.AvatarKey, &r.ClassNames, &r.OtherClassNames, &r.RoleName, &r.RoleBuiltinKey, &r.ThreadExists, &r.CanPost)
			return withNames(r), err
		})
	if err != nil {
		return nil, fmt.Errorf("messaging: colleague recipients: %w", err)
	}
	return rows, nil
}

func withNames(r domain.Recipient) domain.Recipient {
	if r.ClassNames == nil {
		r.ClassNames = []string{}
	}
	if r.OtherClassNames == nil {
		r.OtherClassNames = []string{}
	}
	return r
}
