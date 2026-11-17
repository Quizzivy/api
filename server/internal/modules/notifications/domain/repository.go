package domain

import (
	"context"
	"time"
)

// Repository stores notifications and preferences. Every read and every
// write but Upsert and DeleteBefore is bounded by the user it is given: it
// never returns or changes another user's row, whatever ids it is handed.
type Repository interface {
	// Upsert writes n in one statement: a new row, or n merged into the row
	// its user holds under the same dedupe key, which becomes unread. It
	// writes nothing, and reports false, when the user switched off the event
	// that governs n's kind.
	Upsert(ctx context.Context, n Notice) (bool, error)
	// List returns up to q.Size() of q.UserID's notifications older than
	// q.Before, newest first, and where the next page starts.
	List(ctx context.Context, q ListQuery) (Page, error)
	// MarkRead marks those of ids that are userID's, unread and of audience's
	// kinds. The empty Audience, here and in the two methods below, means
	// every kind.
	MarkRead(ctx context.Context, userID string, ids []string, audience Audience) error
	// MarkKeyRead marks read the notification userID holds under key when it
	// is unread and was last written no later than writtenBy, so that a
	// notice written after that moment stays unread.
	MarkKeyRead(ctx context.Context, userID, key string, writtenBy time.Time) error
	// MarkAllRead marks every unread notification userID has of audience's
	// kinds.
	MarkAllRead(ctx context.Context, userID string, audience Audience) error
	// Unread counts userID's unread notifications of audience's kinds.
	Unread(ctx context.Context, userID string, audience Audience) (int, error)
	// Preferences returns the switches userID has saved, and only those.
	Preferences(ctx context.Context, userID string) ([]Preference, error)
	// SavePreferences stores prefs for userID and returns them as stored.
	SavePreferences(ctx context.Context, userID string, prefs []Preference) ([]Preference, error)
	// DeleteBefore deletes every notification created before cutoff (first
	// written, or last renewed under Renew), and returns how many went.
	DeleteBefore(ctx context.Context, cutoff time.Time) (int64, error)
	// Due reads the notices userID has earned by the passing of time alone, as
	// of now: the moments from DueLookback before now up to now, judged
	// against the windows, closes and attempts as they stand. It changes
	// nothing.
	Due(ctx context.Context, userID string, now time.Time) ([]Notice, error)
	// InsertAbsent writes each of notices, which are all userID's, whose
	// dedupe key userID does not hold, and returns how many it wrote. A
	// notice whose event the user switched off is not written. A row already
	// there is left exactly as it is, read or not.
	InsertAbsent(ctx context.Context, userID string, notices []Notice) (int, error)
}

// Scheduled is the store of the notification scheduler (T-R7.14), which reads
// every user's due moments at once and writes their emails, and the weekly
// summary's in-app item, outside any request.
type Scheduled interface {
	// Upcoming reads the moments after now that the next run waits for.
	Upcoming(ctx context.Context, now time.Time, weekly WeeklyOn) (Upcoming, error)
	// Locked runs fn in one transaction that holds the scheduler's advisory
	// lock (73819, 42), and reports false, without running fn, while another
	// transaction holds it. The outbox is woken once fn's writes commit.
	Locked(ctx context.Context, fn func(ScheduledRun) error) (bool, error)
}

// WeeklyOn says which channels of the weekly summary can be on: its email
// unless NOTIFY_EMAIL_EVENTS turned it off, and the in-app item always.
type WeeklyOn struct {
	Email bool
}

// ScheduledRun is one run of the scheduler, inside Scheduled.Locked's
// transaction.
type ScheduledRun interface {
	// DueEmails reads the emails whose moment lies in (now - ScheduleLookback,
	// now] and that the outbox does not hold yet: each student's reminders a
	// day and an hour before their own close, under T-R4.10b's rule (still
	// open to them, not handed in, an attempt left; the day's only while the
	// hour's is not yet due, and neither before the assignment was
	// available); each teacher's warning an hour before an assignment's own
	// close, while a student they reach whose close it is has not handed in;
	// and each graded paper's result that an after-close release showed at
	// the student's close.
	DueEmails(ctx context.Context, now time.Time) ([]Email, error)
	// WeeklyTeachers reads the enabled staff members with an active class
	// whose summary is on in a channel weekly leaves on, with their zone.
	WeeklyTeachers(ctx context.Context, weekly WeeklyOn) ([]WeeklyTeacher, error)
	// WeeklyHeld reports whether teacherID's summary of week is already in
	// the outbox, and already in their notifications.
	WeeklyHeld(ctx context.Context, teacherID string, week Week) (email, inApp bool, err error)
	// Summarise reads teacherID's figures for week on their active classes.
	Summarise(ctx context.Context, teacherID string, week Week) (WeeklyFigures, error)
	// Enqueue writes e unless the recipient's switch is off or the outbox
	// already holds its key, and reports whether it wrote a row.
	Enqueue(ctx context.Context, e Email) (bool, error)
	// Publish writes the weekly summary n in the app while its switch is on,
	// off by default, and leaves a row already held under its key as it is.
	Publish(ctx context.Context, n Notice) (bool, error)
}

// WeeklyTeacher is a staff member whose weekly summary is on: their id, the
// zone their Mondays fall in, and which channels they will be sent it in.
type WeeklyTeacher struct {
	ID    string
	Zone  string
	Email bool
	InApp bool
}
