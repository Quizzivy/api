// Package repositories stores the consultation requests in app.leads, writes the notification of a stored one to the email outbox in the same transaction, and audits an Admin's change or removal in the statement that makes it.
package repositories

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"quizzivy/internal/modules/leads/domain"
	"quizzivy/internal/platform/db"
	"quizzivy/internal/platform/email"
	"quizzivy/internal/shared/opt"
	"quizzivy/internal/shared/paging"
)

// Outbox is the email outbox a notification is written to: the platform's
// Outbox, which writes inside the caller's transaction and wakes this
// process's dispatcher.
type Outbox interface {
	Enqueue(ctx context.Context, q db.Querier, m email.Mail) (string, bool, error)
	Notify()
}

// Postgres keeps the requests in app.leads. A nil outbox stores a request and
// tells nobody.
type Postgres struct {
	db.Repository
	outbox Outbox
}

// NewPostgres keeps the requests through dbx and writes their notifications to outbox.
func NewPostgres(dbx db.Context, outbox Outbox) *Postgres {
	return &Postgres{Repository: db.NewRepository(dbx), outbox: outbox}
}

var _ domain.Repository = (*Postgres)(nil)

const handlerName = `coalesce(nullif(h.display_name, ''), h.full_name)`

func columns(alias string) string {
	fields := []string{
		"id::text", "full_name", "phone_digits", "interests", "course_for", "call_time", "locale",
		"privacy_notice_version", "consented_at", "status", "note", "handled_by::text", "handled_at", "created_at",
	}
	for i, f := range fields {
		fields[i] = alias + "." + f
	}
	return strings.Join(fields, ", ") + ", " + handlerName
}

func scan(row pgx.Row) (domain.Lead, error) {
	var (
		l                   domain.Lead
		status              string
		note, handler, name *string
	)
	err := row.Scan(&l.ID, &l.FullName, &l.PhoneDigits, &l.Interests, &l.CourseFor, &l.CallTime, &l.Locale,
		&l.PrivacyNoticeVersion, &l.ConsentedAt, &status, &note, &handler, &l.HandledAt, &l.CreatedAt, &name)
	if err != nil {
		return domain.Lead{}, err
	}
	l.Status, l.Note = domain.Status(status), opt.Deref(note)
	if l.Interests == nil {
		l.Interests = []string{}
	}
	if handler != nil {
		l.HandledBy = &domain.Handler{ID: *handler, Name: opt.Deref(name)}
	}
	return l, nil
}

// Submit judges the submission against the requests its number already has,
// under a lock on the number, so two submissions at once are judged one after
// the other.
func (p *Postgres) Submit(ctx context.Context, in domain.Submit) (domain.Drop, error) {
	s := in.Submission
	verdict := domain.Stored
	told := 0
	err := p.InTx(ctx, "leads: submit", func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(73821, hashtext($1))`, s.PhoneDigits); err != nil {
			return fmt.Errorf("leads: lock the number: %w", err)
		}
		var recent int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM app.leads WHERE phone_digits = $1 AND created_at > $2`,
			s.PhoneDigits, in.Now.Add(-domain.RepeatWindow)).Scan(&recent); err != nil {
			return fmt.Errorf("leads: count the number's requests: %w", err)
		}
		if verdict = s.Verdict(recent); verdict != domain.Stored {
			return nil
		}
		var id string
		if err := tx.QueryRow(ctx, `
			INSERT INTO app.leads
			       (full_name, phone_digits, interests, course_for, call_time, locale,
			        privacy_notice_version, consented_at, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $8)
			RETURNING id::text`,
			s.FullName, s.PhoneDigits, s.Interests, s.CourseFor, s.CallTime, s.Locale, s.PrivacyNoticeVersion, in.Now).Scan(&id); err != nil {
			return fmt.Errorf("leads: store the request: %w", err)
		}
		var err error
		told, err = p.tell(ctx, tx, id, in)
		return err
	})
	if err != nil {
		return domain.Stored, err
	}
	if told > 0 {
		p.outbox.Notify()
	}
	return verdict, nil
}

type recipient struct {
	address string
	userID  *string
}

func (p *Postgres) tell(ctx context.Context, tx pgx.Tx, id string, in domain.Submit) (int, error) {
	if p.outbox == nil {
		return 0, nil
	}
	recipients, err := db.QueryMany(ctx, tx, `
		SELECT r.address, u.id::text
		  FROM app.organization_settings s
		 CROSS JOIN LATERAL unnest(s.lead_recipients) WITH ORDINALITY AS r(address, position)
		  LEFT JOIN app.users u ON lower(u.email) = r.address
		 ORDER BY r.position`, nil, func(rows pgx.Rows) (recipient, error) {
		var r recipient
		err := rows.Scan(&r.address, &r.userID)
		return r, err
	})
	if err != nil {
		return 0, fmt.Errorf("leads: read the recipients: %w", err)
	}
	s := in.Submission
	params := email.LeadReceivedParams{
		LeadID: id, GivenName: domain.GivenName(s.FullName, s.Locale), Interests: s.Interests,
		CourseFor: s.CourseFor, CallTime: s.CallTime, ReceivedAt: in.Now.UTC(),
	}
	for _, r := range recipients {
		if _, _, err := p.outbox.Enqueue(ctx, tx, email.Mail{ToUserID: opt.Deref(r.userID), To: r.address, Params: params}); err != nil {
			return 0, fmt.Errorf("leads: enqueue the notification: %w", err)
		}
	}
	return len(recipients), nil
}

func filter(q domain.ListQuery) (string, []any) {
	return ` WHERE ($1 = '' OR l.status = $1)
	   AND ($2 = ''
	        OR app.immutable_unaccent(lower(l.full_name)) LIKE '%' || app.immutable_unaccent(lower($2)) || '%' ESCAPE '\'
	        OR ($3 <> '' AND l.phone_digits LIKE '%' || $3 || '%'))`,
		[]any{string(q.Status), db.EscapeLike(q.Q), domain.Digits(q.Q)}
}

// List returns the page of q, newest first, and how many requests match it.
func (p *Postgres) List(ctx context.Context, q domain.ListQuery) ([]domain.Lead, paging.Page, error) {
	number, size, offset := paging.Clamp(q.Page, q.Limit, domain.DefaultPageSize, domain.MaxPageSize)
	where, args := filter(q)
	total, err := db.Count(ctx, p, `SELECT count(*) FROM app.leads l`+where, args...)
	if err != nil {
		return nil, paging.Page{}, fmt.Errorf("leads: count the requests: %w", err)
	}
	leads, err := db.QueryMany(ctx, p, `
		SELECT `+columns("l")+`
		  FROM app.leads l
		  LEFT JOIN app.users h ON h.id = l.handled_by`+where+`
		 ORDER BY l.created_at DESC, l.id DESC
		 LIMIT $4 OFFSET $5`, append(args, size, offset), func(rows pgx.Rows) (domain.Lead, error) { return scan(rows) })
	if err != nil {
		return nil, paging.Page{}, fmt.Errorf("leads: list the requests: %w", err)
	}
	return leads, paging.Page{Number: number, Size: size, Total: total}, nil
}

// Get returns the request id.
func (p *Postgres) Get(ctx context.Context, id string) (domain.Lead, error) {
	lead, err := scan(p.QueryRow(ctx, `
		SELECT `+columns("l")+`
		  FROM app.leads l
		  LEFT JOIN app.users h ON h.id = l.handled_by
		 WHERE l.id = $1::uuid`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Lead{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Lead{}, fmt.Errorf("leads: read the request: %w", err)
	}
	return lead, nil
}

// Update changes the request and audits the change in one statement. A change
// that leaves the row as it is matches no row there, and the request is read
// back as it stands.
func (p *Postgres) Update(ctx context.Context, in domain.Update) (domain.Lead, error) {
	var status *string
	if in.Change.Status != nil {
		status = opt.String(string(*in.Change.Status))
	}
	lead, err := scan(p.QueryRow(ctx, `
		WITH updated AS (
		  UPDATE app.leads l
		     SET status = coalesce($2::text, l.status),
		         note = CASE WHEN $3::boolean THEN $4::text ELSE l.note END,
		         handled_by = CASE WHEN $2::text <> l.status THEN $5::uuid ELSE l.handled_by END,
		         handled_at = CASE WHEN $2::text <> l.status THEN $6::timestamptz ELSE l.handled_at END
		   WHERE l.id = $1::uuid
		     AND ($2::text <> l.status OR ($3::boolean AND $4::text IS DISTINCT FROM l.note))
		  RETURNING l.id, l.full_name, l.phone_digits, l.interests, l.course_for, l.call_time, l.locale,
		            l.privacy_notice_version, l.consented_at, l.status, l.note, l.handled_by, l.handled_at, l.created_at,
		            old.status AS prev_status, old.note IS DISTINCT FROM new.note AS note_changed
		), logged AS (
		  INSERT INTO app.audit_log
		         (actor_user_id, action, entity, entity_id, occurred_at, ip, user_agent, diff)
		  SELECT $5::uuid, 'lead.updated', 'lead', updated.id, $6, $7::inet, $8,
		         jsonb_strip_nulls(jsonb_build_object(
		           'status', CASE WHEN updated.prev_status <> updated.status
		                          THEN jsonb_build_object('old', updated.prev_status, 'new', updated.status) END,
		           'noteChanged', CASE WHEN updated.note_changed THEN true END))
		    FROM updated
		)
		SELECT `+columns("updated")+`
		  FROM updated
		  LEFT JOIN app.users h ON h.id = updated.handled_by`,
		in.ID, status, in.Change.NoteSet, opt.String(in.Change.Note), opt.String(in.ActorID), in.Now,
		opt.String(in.IP), opt.String(in.UserAgent)))
	if errors.Is(err, pgx.ErrNoRows) {
		return p.Get(ctx, in.ID)
	}
	if err != nil {
		return domain.Lead{}, fmt.Errorf("leads: update the request: %w", err)
	}
	return lead, nil
}

// Delete removes the request and audits the removal in one statement.
func (p *Postgres) Delete(ctx context.Context, in domain.Delete) error {
	removed, err := db.Count(ctx, p, `
		WITH removed AS (
		  DELETE FROM app.leads WHERE id = $1::uuid RETURNING id
		), logged AS (
		  INSERT INTO app.audit_log
		         (actor_user_id, action, entity, entity_id, occurred_at, ip, user_agent)
		  SELECT $2::uuid, 'lead.deleted', 'lead', removed.id, $3, $4::inet, $5
		    FROM removed
		)
		SELECT count(*) FROM removed`,
		in.ID, opt.String(in.ActorID), in.Now, opt.String(in.IP), opt.String(in.UserAgent))
	if err != nil {
		return fmt.Errorf("leads: delete the request: %w", err)
	}
	if removed == 0 {
		return domain.ErrNotFound
	}
	return nil
}
