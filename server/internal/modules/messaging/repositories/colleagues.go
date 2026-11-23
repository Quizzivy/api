package repositories

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"quizzivy/internal/modules/messaging/domain"
	"quizzivy/internal/platform/db"
	"quizzivy/internal/shared/limits"
	"quizzivy/internal/shared/opt"
	"quizzivy/internal/shared/retention"
	"quizzivy/internal/shared/visibility"
)

// workspaceRole is the SQL over the users row named by alias that the
// account's role opens the teacher workspace, as the share directory reads
// it (limits.TeacherRole): the Admin console alone opens no Messages.
func workspaceRole(alias string) string { return limits.TeacherRole(alias + ".role_id") }

// canColleague is the one definition of whom the staff member in
// placeholder $1 may write to in a colleague thread (F2, T-F2.4), as a
// boolean SQL expression over the users row named by alias: an enabled
// account that opens the teacher workspace, is not a student and is not
// anonymised; never the staff member. It is domain.Eligibility's
// PostColleague, which TestTheColleagueTableOnTheDatabase holds it to.
func canColleague(alias string) string {
	return fmt.Sprintf(`(%[1]s.disabled_at IS NULL AND %[2]s AND NOT %[3]s AND %[4]s AND $1::uuid IS NOT NULL AND %[1]s.id <> $1::uuid)`,
		alias, workspaceRole(alias), visibility.StudentLike(alias), retention.NotAnonymousOf(alias))
}

// pairOf is the condition that the conversation v is the colleague thread of
// the two staff members the SQL expressions a and b name, whichever writes
// first: the pair is stored low-first, so the unique index finds it.
func pairOf(a, b string) string {
	return fmt.Sprintf(`v.kind = 'colleague' AND %[1]s IS NOT NULL AND %[2]s IS NOT NULL
		AND v.colleague_low_id = least(%[1]s, %[2]s) AND v.colleague_high_id = greatest(%[1]s, %[2]s)`, a, b)
}

// colleagueOf is the other member of the colleague thread v for the staff
// member in placeholder $n.
func colleagueOf(n int) string {
	return fmt.Sprintf(`CASE WHEN v.colleague_low_id = $%[1]d::uuid THEN v.colleague_high_id ELSE v.colleague_low_id END`, n)
}

// mineColleague is the condition that the staff member in placeholder $n is
// one of the colleague thread v's pair, each side served by its own index.
func mineColleague(n int) string {
	return fmt.Sprintf(`v.kind = 'colleague' AND (v.colleague_low_id = $%[1]d::uuid OR v.colleague_high_id = $%[1]d::uuid)`, n)
}

var colleagueFactsSQL = `
	SELECT u.disabled_at IS NULL, ` + workspaceRole("u") + `, ` + visibility.StudentLike("u") + `, NOT (` + retention.NotAnonymousOf("u") + `),
	       EXISTS (SELECT 1 FROM app.conversations v WHERE ` + pairOf("$1::uuid", "$2::uuid") + `)
	  FROM app.users u
	 WHERE u.id = $2::uuid`

// ColleagueFacts reads what decides staffID's colleague thread with
// colleagueID (domain.Repository).
func (p *Postgres) ColleagueFacts(ctx context.Context, staffID, colleagueID string) (domain.ColleagueFacts, error) {
	return colleagueFacts(ctx, p, staffID, colleagueID)
}

func colleagueFacts(ctx context.Context, q db.Querier, staffID, colleagueID string) (domain.ColleagueFacts, error) {
	f := domain.ColleagueFacts{Exists: true, Self: staffID == colleagueID}
	err := q.QueryRow(ctx, colleagueFactsSQL, opt.String(staffID), colleagueID).
		Scan(&f.Enabled, &f.Workspace, &f.Student, &f.Anonymised, &f.Conversation)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ColleagueFacts{Self: f.Self}, nil
	}
	if err != nil {
		return domain.ColleagueFacts{}, fmt.Errorf("messaging: colleague facts: %w", err)
	}
	return f, nil
}

func colleagueConversation(ctx context.Context, q db.Querier, staffID, colleagueID string) (string, error) {
	return conversationID(ctx, q, `SELECT v.id::text FROM app.conversations v WHERE `+pairOf("$1::uuid", "$2::uuid"), opt.String(staffID), opt.String(colleagueID))
}

// Colleague reads colleagueID as a colleague thread's header shows them: the
// name, the photo key, the role's stored name and built-in key, and whether
// the account is disabled; never the email (domain.Repository).
func (p *Postgres) Colleague(ctx context.Context, colleagueID string) (domain.ColleaguePeer, error) {
	var c domain.ColleaguePeer
	err := p.QueryRow(ctx, `
		SELECT u.id::text, `+nameOf("u")+`, u.avatar_key, r.name, r.builtin_key, u.disabled_at IS NOT NULL
		  FROM app.users u JOIN app.roles r ON r.id = u.role_id
		 WHERE u.id = $1::uuid`, colleagueID).
		Scan(&c.ID, &c.Name, &c.AvatarKey, &c.RoleName, &c.RoleBuiltinKey, &c.Disabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ColleaguePeer{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.ColleaguePeer{}, fmt.Errorf("messaging: colleague: %w", err)
	}
	return c, nil
}

func colleagueRows() string {
	return `
	SELECT u.id::text, ` + nameOf("u") + `, u.avatar_key, r.name, r.builtin_key, false, u.disabled_at IS NOT NULL,
	       v.last_message_at, last.body, last.original_filename, coalesce(last.mine, false), ` + unread + `
	  FROM app.conversations v
	  JOIN app.users u ON u.id = ` + colleagueOf(1) + `
	  JOIN app.roles r ON r.id = u.role_id
	  ` + newest + `
	 WHERE ` + mineColleague(1) + ` AND (` + retention.NotAnonymousOf("u") + `)
	   AND ($2::text = '' OR ` + contains(`u.full_name`, 2) + ` OR ` + contains(`coalesce(u.display_name, '')`, 2) + `)
	 ORDER BY v.last_message_at DESC, ` + folded(nameOf("u")) + `, u.id`
}

func colleagueCandidateRows() string {
	return `
	SELECT u.id::text, ` + nameOf("u") + `, u.avatar_key, r.name, r.builtin_key, true, false,
	       NULL::timestamptz, NULL::text, NULL::text, false, 0
	  FROM app.users u
	  JOIN app.roles r ON r.id = u.role_id
	 WHERE ` + canColleague("u") + `
	   AND NOT EXISTS (SELECT 1 FROM app.conversations v WHERE ` + pairOf("$1::uuid", "u.id") + `)
	   AND (` + contains(`u.full_name`, 2) + ` OR ` + contains(`coalesce(u.display_name, '')`, 2) + `)
	 ORDER BY ` + folded(nameOf("u")) + `, u.id
	 LIMIT ` + fmt.Sprint(domain.MaxCandidates)
}

// ColleagueThreads returns the Colleagues tab's rows for f
// (domain.Repository): the caller's colleague threads, newest activity
// first, a disabled colleague's included; then, with a query, at most
// MaxCandidates colleagues the caller may write to and has no thread with,
// by name. A query matches the colleague's names, accent-insensitively.
// scope.all opens no one else's thread (D17).
func (p *Postgres) ColleagueThreads(ctx context.Context, f domain.ColleagueFilter) ([]domain.ColleagueThread, error) {
	query := strings.TrimSpace(f.Query)
	args := []any{opt.String(f.ActorID), db.EscapeLike(query)}
	out, err := db.QueryMany(ctx, p, colleagueRows(), args, scanColleagueThread)
	if err != nil {
		return nil, fmt.Errorf("messaging: colleague threads: %w", err)
	}
	if query == "" {
		return out, nil
	}
	candidates, err := db.QueryMany(ctx, p, colleagueCandidateRows(), args, scanColleagueThread)
	if err != nil {
		return nil, fmt.Errorf("messaging: colleague candidates: %w", err)
	}
	return append(out, candidates...), nil
}

func scanColleagueThread(rows pgx.Rows) (domain.ColleagueThread, error) {
	var t domain.ColleagueThread
	var body, filename *string
	var at *time.Time
	var mine bool
	if err := rows.Scan(&t.ColleagueID, &t.Title, &t.AvatarKey, &t.RoleName, &t.RoleBuiltinKey, &t.Candidate, &t.Disabled,
		&at, &body, &filename, &mine, &t.Unread); err != nil {
		return domain.ColleagueThread{}, err
	}
	t.LastMessageAt = at
	if at != nil {
		t.Preview = domain.PreviewOf(body, filename, mine)
	}
	return t, nil
}

// ColleaguePage reads one page of staffID's thread with colleagueID, empty
// when none exists yet (domain.Repository).
func (p *Postgres) ColleaguePage(ctx context.Context, staffID, colleagueID string, cursor domain.Cursor) (domain.Page, error) {
	conversation, err := colleagueConversation(ctx, p, staffID, colleagueID)
	if err != nil {
		return domain.Page{}, err
	}
	return p.page(ctx, conversation, "", cursor)
}

// PostColleague writes post to staffID's thread with colleagueID in one
// transaction (domain.Repository). The first post makes the thread, its pair
// ordered low-first, ON CONFLICT on the pair's unique index, so two
// colleagues writing first at once make one thread; then, as every post
// does, the thread's row is locked before the message's id is made.
func (p *Postgres) PostColleague(ctx context.Context, staffID, colleagueID string, post domain.Post) (domain.Posted, error) {
	rules := domain.Eligibility{}
	return p.post(ctx, "messaging: post to a colleague", post, func(ctx context.Context, tx pgx.Tx) (target, error) {
		return pairTarget(ctx, tx, rules.ReadColleague, rules.PostColleague,
			func(ctx context.Context, q db.Querier) (domain.ColleagueFacts, error) {
				return colleagueFacts(ctx, q, staffID, colleagueID)
			},
			func(ctx context.Context, tx pgx.Tx) (string, error) {
				return colleagueConversation(ctx, tx, staffID, colleagueID)
			},
			`INSERT INTO app.conversations (kind, colleague_low_id, colleague_high_id, last_message_at)
			 VALUES ('colleague', least($1::uuid, $2::uuid), greatest($1::uuid, $2::uuid), now())
			 ON CONFLICT (colleague_low_id, colleague_high_id) WHERE kind = 'colleague' DO NOTHING`, staffID, colleagueID)
	})
}

// MarkColleagueRead moves the reader's pointer in staffID's thread with
// colleagueID forward to the mark's message (domain.Repository), as
// MarkDirectRead does.
func (p *Postgres) MarkColleagueRead(ctx context.Context, staffID, colleagueID string, mark domain.ReadMark) (domain.ReadState, error) {
	return p.markRead(ctx, pairOf("$3::uuid", "$4::uuid"), mark, opt.String(staffID), opt.String(colleagueID))
}
