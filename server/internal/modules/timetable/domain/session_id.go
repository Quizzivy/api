package domain

import (
	"strings"
	"time"

	"github.com/google/uuid"
)

const occurrenceDate = "20060102"

// SessionID is a session as the contract names it, opaquely: a one-off's row
// id, or a weekly occurrence as its series and the date it was scheduled for,
// which stays its id when the session moves and whether or not it has a row.
type SessionID struct {
	OneOff   string
	SeriesID string
	Date     time.Time
}

// OneOffID names the one-off session whose row is id.
func OneOffID(id string) SessionID { return SessionID{OneOff: id} }

// OccurrenceID names the occurrence of seriesID scheduled for date.
func OccurrenceID(seriesID string, date time.Time) SessionID {
	return SessionID{SeriesID: seriesID, Date: Day(date)}
}

// ParseSessionID reads raw as a canonical lower-case uuid (a one-off) or as
// `{seriesId}.{yyyymmdd}` with a canonical uuid and a real date (an
// occurrence). Anything else is ErrNotFound: the id names nothing.
func ParseSessionID(raw string) (SessionID, error) {
	head, tail, dotted := strings.Cut(raw, ".")
	if !canonical(head) {
		return SessionID{}, ErrNotFound
	}
	if !dotted {
		return OneOffID(head), nil
	}
	date, err := time.Parse(occurrenceDate, tail)
	if err != nil || date.Format(occurrenceDate) != tail {
		return SessionID{}, ErrNotFound
	}
	return OccurrenceID(head, date), nil
}

func canonical(s string) bool {
	id, err := uuid.Parse(s)
	return err == nil && id.String() == s
}

// Occurrence reports whether id names an occurrence of a series.
func (id SessionID) Occurrence() bool { return id.SeriesID != "" }

// String is id as the contract writes it.
func (id SessionID) String() string {
	if id.Occurrence() {
		return id.SeriesID + "." + id.Date.Format(occurrenceDate)
	}
	return id.OneOff
}
