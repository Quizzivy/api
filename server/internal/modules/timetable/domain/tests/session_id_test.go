package domain_test

import (
	"errors"
	"testing"

	"quizzivy/internal/modules/timetable/domain"
)

// TestSessionIDsRoundTrip formats and parses both forms: a one-off's uuid and
// an occurrence's {seriesId}.{yyyymmdd}.
func TestSessionIDsRoundTrip(t *testing.T) {
	series := "019535d9-3df7-79fb-b466-fa907fa17f9e"
	for _, id := range []domain.SessionID{
		domain.OneOffID("019535d9-3df7-79fb-b466-fa907fa17f00"),
		domain.OccurrenceID(series, date("2026-12-31")),
		domain.OccurrenceID(series, date("2028-02-29")),
	} {
		parsed, err := domain.ParseSessionID(id.String())
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		if parsed != id || parsed.String() != id.String() || parsed.Occurrence() != id.Occurrence() {
			t.Errorf("%s parsed as %+v", id, parsed)
		}
	}
	if got := domain.OccurrenceID(series, date("2026-09-22")).String(); got != series+".20260922" {
		t.Errorf("an occurrence formats as %s", got)
	}
}

// TestAnythingElseNamesNothing refuses every other spelling with ErrNotFound,
// which the transport answers 404.
func TestAnythingElseNamesNothing(t *testing.T) {
	series := "019535d9-3df7-79fb-b466-fa907fa17f9e"
	for _, raw := range []string{
		"", "abc", series + ".", series + ".2026092", series + ".202609222", series + ".20260231", series + ".2026-09-22",
		series + ".20260922.1", "019535D9-3DF7-79FB-B466-FA907FA17F9E", "{" + series + "}", "urn:uuid:" + series,
		series[:35] + "." + "20260922", " " + series, series + ".+0260922",
	} {
		if got, err := domain.ParseSessionID(raw); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("%q parsed as %+v, %v", raw, got, err)
		}
	}
}
