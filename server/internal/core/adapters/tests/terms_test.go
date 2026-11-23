package adapters_test

import (
	"context"
	"testing"
	"time"

	"quizzivy/internal/core/adapters"
	organizationmodel "quizzivy/internal/modules/organization/application/model"
	organizationquery "quizzivy/internal/modules/organization/application/query"
	"quizzivy/internal/shared/cqrs"
)

func TestTermsAsksTheOrganizationForTheDayItIsGiven(t *testing.T) {
	on := time.Date(2026, 9, 1, 0, 30, 0, 0, time.FixedZone("ICT", 7*60*60))
	autumn := organizationmodel.Term{ID: "autumn"}
	var asked []time.Time
	terms := adapters.Terms{
		Current: cqrs.HandlerFunc[organizationquery.CurrentTerm, *organizationmodel.Term](func(_ context.Context, q organizationquery.CurrentTerm) (*organizationmodel.Term, error) {
			asked = append(asked, q.On)
			return &autumn, nil
		}),
		EndingFrom: cqrs.HandlerFunc[organizationquery.TermsEndingFrom, []organizationmodel.Term](func(_ context.Context, q organizationquery.TermsEndingFrom) ([]organizationmodel.Term, error) {
			asked = append(asked, q.On)
			return []organizationmodel.Term{autumn}, nil
		}),
	}
	if got, err := terms.CurrentTerm(context.Background(), on); err != nil || got == nil || got.ID != "autumn" {
		t.Errorf("CurrentTerm: %+v (%v), want the organization's answer", got, err)
	}
	if got, err := terms.TermsEndingFrom(context.Background(), on); err != nil || len(got) != 1 {
		t.Errorf("TermsEndingFrom: %+v (%v), want the organization's answer", got, err)
	}
	if len(asked) != 2 || !asked[0].Equal(on) || asked[0].Location() != on.Location() || !asked[1].Equal(on) {
		t.Errorf("the organization was asked for %v, want the instant as given, zone kept, twice", asked)
	}
}
