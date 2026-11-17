// Package provenance names the last write of a test or a bank question, so a
// co-editor's stale write can say who saved last and when (T-R7.5). Every
// write of app.tests and app.questions records its actor in updated_by; the
// row's updated_at is the trigger's.
package provenance

import (
	"errors"
	"time"
)

// Editor is who made a write: an account id and the name a line of a page
// shows for it.
type Editor struct {
	ID   string
	Name string
}

// LastWrite is a row's last write. By is nil when no one is recorded: a row
// no R7 write has touched (the R6 binary leaves the column alone), an account
// since deleted, or an ownership transfer, which is no one's edit. At is the
// row's updated_at.
//
// By is wrong, not nil, for a row the R6 binary updated after an R7 write:
// the trigger moves updated_at and updated_by keeps the R7 writer. On the way
// in that is only a row R7 wrote while R6 still served, during the deploy's
// overlap; after a rollback to v0.13 it is every row v0.13 updates, until R7
// next writes it. Migration 00161 says how to clear them on the way back.
type LastWrite struct {
	By *Editor
	At time.Time
}

// Columns selects, over the row alias of app.tests or app.questions, what a
// Row scans: who last wrote it, their display name, and when.
func Columns(alias string) string {
	return alias + `.updated_by::text, (SELECT coalesce(nullif(u.display_name, ''), u.full_name) FROM app.users u WHERE u.id = ` +
		alias + `.updated_by), ` + alias + `.updated_at`
}

// Row is the scan target of Columns.
type Row struct {
	id, name *string
	at       time.Time
}

// Dest is where a scan of Columns writes, in order.
func (r *Row) Dest() []any { return []any{&r.id, &r.name, &r.at} }

// LastWrite is the write r read.
func (r Row) LastWrite() LastWrite {
	last := LastWrite{At: r.at}
	if r.id != nil {
		last.By = &Editor{ID: *r.id}
		if r.name != nil {
			last.By.Name = *r.name
		}
	}
	return last
}

// StaleError is a write refused because its row changed after the revision
// the caller read. It is Sentinel, the module's own stale-write error, to
// errors.Is, and Last names the write the caller had not seen.
type StaleError struct {
	Sentinel error
	Last     LastWrite
}

func (e *StaleError) Error() string { return e.Sentinel.Error() }

func (e *StaleError) Unwrap() error { return e.Sentinel }

// Stale is sentinel naming last.
func Stale(sentinel error, last LastWrite) error {
	return &StaleError{Sentinel: sentinel, Last: last}
}

// Of is the last write err names, when err is or wraps a StaleError.
func Of(err error) (LastWrite, bool) {
	var stale *StaleError
	if errors.As(err, &stale) {
		return stale.Last, true
	}
	return LastWrite{}, false
}

// Named is err naming last when err is sentinel without a last write of its
// own, and err unchanged otherwise: a check that only knows a revision moved
// borrows the write its caller read under the same lock.
func Named(err, sentinel error, last LastWrite) error {
	if !errors.Is(err, sentinel) {
		return err
	}
	if _, named := Of(err); named {
		return err
	}
	return Stale(sentinel, last)
}
