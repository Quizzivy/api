package tests

import (
	"context"
	"errors"
	"testing"
	"time"

	"quizzivy/internal/modules/organization/application/model"
	"quizzivy/internal/modules/organization/application/query"
	"quizzivy/internal/modules/organization/domain"
)

// fakeTerms holds terms in memory, for the reads; the writes are the
// database's and are tested against it (terms_test.go).
type fakeTerms struct {
	domain.Terms
	terms []domain.Term
	asked []time.Time
}

func (f *fakeTerms) List(context.Context) ([]domain.Term, error) { return f.terms, nil }

func (f *fakeTerms) On(_ context.Context, day time.Time) (*domain.Term, error) {
	f.asked = append(f.asked, day)
	for _, t := range f.terms {
		if t.Holds(day) {
			return &t, nil
		}
	}
	return nil, nil
}

func (f *fakeTerms) EndingFrom(_ context.Context, day time.Time) ([]domain.Term, error) {
	f.asked = append(f.asked, day)
	var out []domain.Term
	for _, t := range f.terms {
		if !t.EndsOn.Before(day) {
			out = append(out, t)
		}
	}
	return out, nil
}

type fakeZones map[string]string

func (z fakeZones) ZoneOf(_ context.Context, userID string) (string, error) {
	if zone, ok := z[userID]; ok {
		return zone, nil
	}
	return "", errors.New("no such account")
}

func utcDay(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

// The list states each term on the viewer's today, counted in the zone their
// profile names, else Asia/Ho_Chi_Minh: at 17:30 UTC on 31 August it is
// already 1 September in Ho Chi Minh City, where the autumn term has begun
// and the summer one has ended, while in UTC both still stand as they did.
func TestTheListStatesEachTermOnTheViewersToday(t *testing.T) {
	summer := domain.Term{ID: "summer", Name: "Hè", StartsOn: utcDay(2026, 6, 1), EndsOn: utcDay(2026, 8, 31)}
	autumn := domain.Term{ID: "autumn", Name: "Học kỳ 1", StartsOn: utcDay(2026, 9, 1), EndsOn: utcDay(2027, 1, 15)}
	app, c := build(t, &fakeRepo{settings: stored()})
	c.now = time.Date(2026, 8, 31, 17, 30, 0, 0, time.UTC)
	app.WithTerms(&fakeTerms{terms: []domain.Term{autumn, summer}}).
		WithZones(fakeZones{"hcmc": "Asia/Ho_Chi_Minh", "utc": "UTC", "unset": ""})

	cases := map[string]map[string]domain.TermState{
		"hcmc":  {"autumn": domain.TermCurrent, "summer": domain.TermPast},
		"unset": {"autumn": domain.TermCurrent, "summer": domain.TermPast},
		"utc":   {"autumn": domain.TermUpcoming, "summer": domain.TermCurrent},
	}
	for viewer, want := range cases {
		listed, err := app.Queries.ListTerms.Handle(context.Background(), query.ListTerms{ViewerID: viewer})
		if err != nil {
			t.Fatal(err)
		}
		if len(listed) != 2 || listed[0].ID != "autumn" {
			t.Fatalf("%s: listed %+v, want the store's order", viewer, listed)
		}
		for _, term := range listed {
			if term.State != want[term.ID] {
				t.Errorf("%s sees %s as %s, want %s", viewer, term.ID, term.State, want[term.ID])
			}
		}
	}
	if _, err := app.Queries.ListTerms.Handle(context.Background(), query.ListTerms{ViewerID: "gone"}); err == nil {
		t.Error("a viewer whose zone cannot be read was answered, want the error")
	}
}

// Another module asks with an instant in its own zone; the term is the one
// that holds that instant's calendar day there.
func TestCurrentTermReadsTheDayOfTheInstantInItsOwnZone(t *testing.T) {
	autumn := domain.Term{ID: "autumn", Name: "Học kỳ 1", StartsOn: utcDay(2026, 9, 1), EndsOn: utcDay(2027, 1, 15)}
	app, _ := build(t, &fakeRepo{settings: stored()})
	terms := &fakeTerms{terms: []domain.Term{autumn}}
	app.WithTerms(terms)
	hcmc := domain.LocationOf("Asia/Ho_Chi_Minh")

	got, err := app.Queries.CurrentTerm.Handle(context.Background(), query.CurrentTerm{On: time.Date(2026, 9, 1, 0, 30, 0, 0, hcmc)})
	if err != nil || got == nil || got.ID != "autumn" {
		t.Fatalf("00:30 on 1 September in Ho Chi Minh City: %+v (%v), want the autumn term", got, err)
	}
	if !terms.asked[0].Equal(utcDay(2026, 9, 1)) {
		t.Errorf("the store was asked for %s, want 2026-09-01", terms.asked[0])
	}
	if got, err := app.Queries.CurrentTerm.Handle(context.Background(), query.CurrentTerm{On: time.Date(2026, 8, 31, 23, 59, 0, 0, hcmc)}); err != nil || got != nil {
		t.Errorf("23:59 on 31 August: %+v (%v), want no term", got, err)
	}
	ending, err := app.Queries.TermsEndingFrom.Handle(context.Background(), query.TermsEndingFrom{On: time.Date(2027, 1, 15, 23, 0, 0, 0, hcmc)})
	if err != nil || len(ending) != 1 {
		t.Errorf("terms ending from its last day: %+v (%v), want the autumn term", ending, err)
	}
	if ending, _ := app.Queries.TermsEndingFrom.Handle(context.Background(), query.TermsEndingFrom{On: utcDay(2027, 1, 16)}); len(ending) != 0 {
		t.Errorf("terms ending from the day after: %+v, want none", ending)
	}
}

func TestAModelTermHoldsItsFirstAndLastDay(t *testing.T) {
	term := model.Term{StartsOn: utcDay(2026, 9, 1), EndsOn: utcDay(2027, 1, 15)}
	hcmc := domain.LocationOf("Asia/Ho_Chi_Minh")
	for on, want := range map[time.Time]bool{
		time.Date(2026, 8, 31, 23, 59, 0, 0, hcmc): false,
		time.Date(2026, 9, 1, 0, 0, 0, 0, hcmc):    true,
		time.Date(2027, 1, 15, 23, 59, 0, 0, hcmc): true,
		time.Date(2027, 1, 16, 0, 0, 0, 0, hcmc):   false,
	} {
		if got := term.Holds(on); got != want {
			t.Errorf("Holds(%s) = %v, want %v", on, got, want)
		}
	}
}

func TestWithoutATermsStoreEveryTermOperationFails(t *testing.T) {
	app, _ := build(t, &fakeRepo{settings: stored()})
	if _, err := app.Queries.ListTerms.Handle(context.Background(), query.ListTerms{}); err == nil {
		t.Error("the list answered without a store")
	}
	if _, err := app.Queries.CurrentTerm.Handle(context.Background(), query.CurrentTerm{On: time.Now()}); err == nil {
		t.Error("the current term answered without a store")
	}
}
