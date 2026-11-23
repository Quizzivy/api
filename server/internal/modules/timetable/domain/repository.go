package domain

import (
	"context"
	"time"

	"quizzivy/internal/shared/access"
)

// Window is what the bulk loaders read: the classes Scope reaches, all of them
// or only ClassIDs when it is not nil, between the instants From and To.
type Window struct {
	Scope    access.Scope
	ClassIDs []string
	From, To time.Time
}

// Writer is who writes, from where and when, as an audit row records it.
type Writer struct {
	ActorID   string
	IP        string
	UserAgent string
	Now       time.Time
}

// NewOneOff is a one-off session to store.
type NewOneOff struct {
	StartsAt        time.Time
	DurationMinutes int
	Room            *string
}

// Created is what a create wrote: the series, or the one-off row.
type Created struct {
	Series *Series
	Row    *Row
}

// SeriesEdit is what a change to a series wrote: the new series (Split.Old
// changed in place, or the one that starts at Split.From) and how many
// per-date rows of the old one were dropped.
type SeriesEdit struct {
	Series  Series
	Dropped int
}

// Repository keeps the series in app.class_session_series and the stored
// sessions in app.class_sessions. A read never writes. Every write runs in one
// transaction under the class row's lock (FOR NO KEY UPDATE), taken only on a
// class Scope reaches, so another teacher's class, session or series answers
// ErrNotFound as a missing one does, and writes its audit row in that
// transaction.
type Repository interface {
	// Series returns, in one query, the series of w's classes that may meet
	// in w (a day either side, for any zone) and every series a row Rows
	// returns names.
	Series(ctx context.Context, w Window) ([]Series, error)
	// Rows returns, in one query, the rows of w's classes that start in w or
	// name an occurrence dated within a day of it, each with HasAttendance.
	Rows(ctx context.Context, w Window) ([]Row, error)
	// ActiveSeries returns, in one query, the series of classes classIDs whose
	// last date is no earlier than the day before now's UTC day, or that have
	// none: every series active in its own zone (Series.ActiveOn), and those
	// that ended yesterday in a zone ahead of UTC. It applies no scope: the
	// caller names classes it has reached already (a class's schedule line).
	ActiveSeries(ctx context.Context, classIDs []string, now time.Time) ([]Series, error)
	// Find resolves id under scope: a one-off's row, or a series and its row
	// for the date when it has one. An occurrence the series does not meet on
	// that date and has no row for, a row id that is not a one-off, and an id
	// outside scope are ErrNotFound.
	Find(ctx context.Context, scope access.Scope, id SessionID) (Found, error)
	// Reaches answers ErrNotFound unless scope reaches class classID, archived
	// or not, in one query.
	Reaches(ctx context.Context, scope access.Scope, classID string) error

	// Create locks classID, refusing ErrClassArchived, and stores the series
	// build makes of the class's own room (nil for none), or, when build
	// returns nil, the one-off, with one session.created row.
	Create(ctx context.Context, scope access.Scope, classID string, build func(classRoom *string) (*Series, *NewOneOff, error), by Writer) (Created, error)
	// EditSession locks the session's class and writes the edit change makes
	// of it: a new or changed row, the row deleted when the edit no longer
	// needs one, or nothing when nothing changed, with one session.cancelled
	// or session.updated row.
	EditSession(ctx context.Context, scope access.Scope, id SessionID, change func(Found) (Edit, error), by Writer) (Found, error)
	// DeleteOneOff locks the one-off's class and removes it with one
	// session.deleted row. An occurrence is ErrInSeries and a session with
	// attendance ErrHasAttendance.
	DeleteOneOff(ctx context.Context, scope access.Scope, id SessionID, by Writer) error
	// EditSeries locks the series' class and the series, and stores the split
	// plan makes of it and of the series' per-date rows, deleting the rows
	// from Split.From on that the old series does not keep (Series.Keeps),
	// with one session_series.updated row; a split with no fields writes
	// nothing.
	EditSeries(ctx context.Context, scope access.Scope, seriesID string, plan func(s Series, rows []Row) (Split, error), by Writer) (SeriesEdit, error)
	// EndSeries locks the series' class and the series and stores the ending
	// plan makes of it: the last date set and the later per-date rows it
	// does not keep deleted, or the series removed, with one
	// session_series.ended row. An occurrence that starts after the last date
	// with attendance, or any with attendance when the series is removed, is
	// ErrHasAttendance, and nothing changes. Removed is nil Series.
	EndSeries(ctx context.Context, scope access.Scope, seriesID string, plan func(Series) (Ending, error), by Writer) (*Series, error)
}
