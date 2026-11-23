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
	f := q.Filter
	f.Audience = domain.AudienceStudents
	f, err := f.Check()
	if err != nil {
		return domain.Recipients{}, err
	}
	if q.Cursor != "" {
		if f.After, err = domain.ParseRecipientCursor(q.Cursor); err != nil {
			return domain.Recipients{}, err
		}
	}
	rows, err := s.Repo.StudentRecipients(ctx, f)
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
