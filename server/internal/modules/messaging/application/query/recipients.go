package query

import (
	"context"

	"quizzivy/internal/modules/messaging/application/internal/support"
	"quizzivy/internal/modules/messaging/domain"
)

// StudentRecipients is one page of the New message picker's students
// (listMessageRecipients, audience students, F2): every enabled student the
// caller may write to, in one of their classes, in another teacher's or in
// none. Filter.All is the caller's class scope, which bounds the class and
// "my classes" filters and nothing else. Cursor is the nextCursor of the page
// before, empty for the first.
type StudentRecipients struct {
	Filter domain.RecipientFilter
	Cursor string
}

type StudentRecipientsHandler struct {
	*support.Service
}

// Handle answers domain.ErrInvalid for a filter the contract refuses or a
// cursor the picker did not give, and domain.ErrNotFound for a class outside
// the caller's class scope.
func (s StudentRecipientsHandler) Handle(ctx context.Context, q StudentRecipients) (domain.Recipients, error) {
	return recipients(ctx, s.Service, domain.AudienceStudents, q.Filter, q.Cursor, s.Repo.StudentRecipients)
}

// ColleagueRecipients is one page of the New message picker's colleagues
// (listMessageRecipients, audience colleagues, T-F2.4): every enabled account
// that opens the teacher workspace and is not a student, never the caller.
// Every staff member gets the same rows; scope.all plays no part.
type ColleagueRecipients struct {
	Filter domain.RecipientFilter
	Cursor string
}

type ColleagueRecipientsHandler struct {
	*support.Service
}

// Handle answers domain.ErrInvalid for a students' filter or a cursor the
// picker did not give.
func (s ColleagueRecipientsHandler) Handle(ctx context.Context, q ColleagueRecipients) (domain.Recipients, error) {
	return recipients(ctx, s.Service, domain.AudienceColleagues, q.Filter, q.Cursor, s.Repo.ColleagueRecipients)
}

// recipients checks f for audience, reads the row its cursor names, then one
// page of read past it, and makes the next page's cursor from the last row
// while more remain.
func recipients(ctx context.Context, s *support.Service, audience domain.Audience, f domain.RecipientFilter, cursor string,
	read func(context.Context, domain.RecipientFilter) ([]domain.Recipient, error)) (domain.Recipients, error) {
	f.Audience = audience
	f, err := f.Check()
	if err != nil {
		return domain.Recipients{}, err
	}
	if cursor != "" {
		if f.After, err = domain.ParseRecipientCursor(cursor); err != nil {
			return domain.Recipients{}, err
		}
	}
	rows, err := read(ctx, f)
	if err != nil {
		return domain.Recipients{}, err
	}
	var out domain.Recipients
	if len(rows) > f.Limit {
		rows = rows[:f.Limit]
		out.Next = domain.RecipientCursor(rows[len(rows)-1].ID)
	}
	signed := map[string]*string{}
	for i := range rows {
		rows[i].AvatarURL = s.Sign(ctx, rows[i].AvatarKey, signed)
	}
	out.Rows = rows
	return out, nil
}
