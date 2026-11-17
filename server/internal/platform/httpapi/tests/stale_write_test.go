package httpapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"quizzivy/internal/platform/httpapi"
	"quizzivy/internal/shared/provenance"
)

var errEditedElsewhere = errors.New("edited elsewhere")

// staleDetails is a StaleWrite answer as a client reads it off the wire.
func staleDetails(t *testing.T, err error) (code string, details map[string]any) {
	t.Helper()
	encoded, marshal := json.Marshal(httpapi.StaleWrite(context.Background(), "Saved elsewhere.", err))
	if marshal != nil {
		t.Fatal(marshal)
	}
	var wire struct {
		Error struct {
			Code    string         `json:"code"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatal(err)
	}
	return wire.Error.Code, wire.Error.Details
}

// TestAStaleWriteNamesWhoSavedLast: a 409 STALE_WRITE carries
// details.updatedBy as a PersonRef and details.updatedAt, through any
// wrapping of the error; a write no one is recorded on is updatedBy null.
func TestAStaleWriteNamesWhoSavedLast(t *testing.T) {
	at := time.Date(2026, 11, 16, 3, 4, 5, 123456000, time.UTC)
	const editor = "019535d9-3df7-79fb-b466-fa907fa17f9e"
	named := fmt.Errorf("tests: autosave: %w", provenance.Stale(errEditedElsewhere,
		provenance.LastWrite{By: &provenance.Editor{ID: editor, Name: "Cô Giàu"}, At: at}))

	code, details := staleDetails(t, named)
	by, _ := details["updatedBy"].(map[string]any)
	if code != "STALE_WRITE" || by["id"] != editor || by["name"] != "Cô Giàu" || details["updatedAt"] != "2026-11-16T03:04:05.123456Z" {
		t.Errorf("a named stale write answers %s with %v", code, details)
	}
	if !errors.Is(named, errEditedElsewhere) {
		t.Error("a named stale write is no longer its module's sentinel")
	}

	code, details = staleDetails(t, provenance.Stale(errEditedElsewhere, provenance.LastWrite{At: at}))
	if value, present := details["updatedBy"]; code != "STALE_WRITE" || !present || value != nil || details["updatedAt"] != "2026-11-16T03:04:05.123456Z" {
		t.Errorf("a stale write no one is recorded on answers %s with %v, want updatedBy null", code, details)
	}

	if code, details = staleDetails(t, errEditedElsewhere); code != "STALE_WRITE" || details != nil {
		t.Errorf("a stale write that names nothing answers %s with %v, want no details", code, details)
	}
}

// TestNamedKeepsTheWriteAStaleErrorAlreadyNames: a check that only knows a
// revision moved borrows the write its caller read, and leaves any other
// error, and one already named, as it was.
func TestNamedKeepsTheWriteAStaleErrorAlreadyNames(t *testing.T) {
	read := provenance.LastWrite{By: &provenance.Editor{ID: "a"}, At: time.Unix(10, 0)}
	own := provenance.LastWrite{By: &provenance.Editor{ID: "b"}, At: time.Unix(20, 0)}
	if last, ok := provenance.Of(provenance.Named(errEditedElsewhere, errEditedElsewhere, read)); !ok || last.By.ID != "a" {
		t.Errorf("a bare stale write borrowed %+v (%v)", last, ok)
	}
	if last, _ := provenance.Of(provenance.Named(provenance.Stale(errEditedElsewhere, own), errEditedElsewhere, read)); last.By.ID != "b" {
		t.Errorf("a named stale write was renamed to %+v", last)
	}
	other := errors.New("something else")
	if got := provenance.Named(other, errEditedElsewhere, read); got != other {
		t.Errorf("another error became %v", got)
	}
}
