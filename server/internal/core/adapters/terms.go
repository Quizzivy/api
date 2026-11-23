package adapters

import (
	"context"
	"time"

	organizationmodel "quizzivy/internal/modules/organization/application/model"
	organizationquery "quizzivy/internal/modules/organization/application/query"
	"quizzivy/internal/shared/cqrs"
)

// Terms answers the organization module's terms to a module that counts by
// them, through the organization's CurrentTerm and TermsEndingFrom queries:
// the timetable's series ends and attendance rates (T-R8.4, T-R8.6), the
// calendar (T-R8.5) and R9's gradebook. Wire it from the organization
// application's Queries.
type Terms struct {
	Current    cqrs.QueryHandler[organizationquery.CurrentTerm, *organizationmodel.Term]
	EndingFrom cqrs.QueryHandler[organizationquery.TermsEndingFrom, []organizationmodel.Term]
}

// CurrentTerm is the term that holds the calendar day of on, read in on's own
// location, or nil: the current term when on is today in the caller's zone,
// and the term a date falls in otherwise, such as a series' first date.
func (a Terms) CurrentTerm(ctx context.Context, on time.Time) (*organizationmodel.Term, error) {
	return a.Current.Handle(ctx, organizationquery.CurrentTerm{On: on})
}

// TermsEndingFrom is every term whose last day is the calendar day of on or
// later, earliest first.
func (a Terms) TermsEndingFrom(ctx context.Context, on time.Time) ([]organizationmodel.Term, error) {
	return a.EndingFrom.Handle(ctx, organizationquery.TermsEndingFrom{On: on})
}
