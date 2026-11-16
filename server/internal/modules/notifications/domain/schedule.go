package domain

import (
	"fmt"
	"time"
)

const (
	// ScheduleLookback is how far back the scheduler still sends an email
	// whose moment has passed (T-R7.14): a run processes every moment in
	// (now - ScheduleLookback, now], so a restart within it catches up once.
	ScheduleLookback = 26 * time.Hour
	// ScheduleIdle is the longest the scheduler sleeps between two runs:
	// with nothing due it still wakes this often, and nothing else wakes it.
	ScheduleIdle = 6 * time.Hour
	// WeeklySummaryHour is the hour of Monday, in the teacher's own zone, at
	// which their summary of the week before falls due.
	WeeklySummaryHour = 7
	// DefaultZone is the calendar zone of an account that has not chosen
	// one, as identity's EffectiveZone answers it.
	DefaultZone = "Asia/Ho_Chi_Minh"
)

// Upcoming is what the next run of the scheduler waits for, as the store
// reads it after now: the earliest due-soon moment of any student (a close,
// overrides included, less a day or an hour), the earliest closing warning
// (an assignment's own close less an hour), and the earliest after-close
// release of results (a close, overrides included), each zero when there is
// none; and the zones of the teachers whose weekly summary is on.
type Upcoming struct {
	DueSoon time.Time
	Closing time.Time
	Release time.Time
	Zones   []*time.Location
}

// Next is the earliest of u's moments after now among those whose emails
// sends leaves on (by EmailTemplate.Event; nil sends every one), the
// weekly summaries' next Monday 07:00 in each zone included, and false when
// there is none: the scheduler then waits ScheduleIdle.
func (u Upcoming) Next(now time.Time, sends func(event string) bool) (time.Time, bool) {
	on := func(t EmailTemplate) bool { return sends == nil || sends(t.Event()) }
	var next time.Time
	consider := func(at time.Time) {
		if at.After(now) && (next.IsZero() || at.Before(next)) {
			next = at
		}
	}
	for template, at := range map[EmailTemplate]time.Time{
		EmailAssignmentDueSoon: u.DueSoon, EmailAssignmentClosing: u.Closing, EmailResultReady: u.Release,
	} {
		if on(template) && !at.IsZero() {
			consider(at)
		}
	}
	for _, zone := range u.Zones {
		consider(NextWeekly(now, zone))
	}
	return next, !next.IsZero()
}

// Zone loads name as identity's EffectiveZone accepts a stored zone, and
// answers false for one that names none or that this host cannot load.
func Zone(name string) (*time.Location, bool) {
	if name == "" || name == "Local" {
		return nil, false
	}
	zone, err := time.LoadLocation(name)
	return zone, err == nil
}

// WeeklyMoment is Monday 07:00, in zone, of the ISO week now falls in there.
// It is after now on a Monday before seven.
func WeeklyMoment(now time.Time, zone *time.Location) time.Time {
	local := now.In(zone)
	back := (int(local.Weekday()) + 6) % 7
	monday := time.Date(local.Year(), local.Month(), local.Day()-back, 0, 0, 0, 0, zone)
	return time.Date(monday.Year(), monday.Month(), monday.Day(), WeeklySummaryHour, 0, 0, 0, zone)
}

// NextWeekly is the first Monday 07:00 in zone after now.
func NextWeekly(now time.Time, zone *time.Location) time.Time {
	at := WeeklyMoment(now, zone)
	if !at.After(now) {
		at = time.Date(at.Year(), at.Month(), at.Day()+7, WeeklySummaryHour, 0, 0, 0, zone)
	}
	return at
}

// Week is the week a teacher's summary covers: Monday 00:00 to the next
// Monday 00:00 in their zone, the week before the Monday it is sent on.
type Week struct {
	From, To time.Time
}

// LastWeek is the week before the one now falls in, in zone, and the moment
// its summary fell due, Monday 07:00 of the week now falls in. On a Monday
// before seven that moment is still to come, and so is the week's; the week
// before that one is then the latest whose summary is due.
func LastWeek(now time.Time, zone *time.Location) (Week, time.Time) {
	due := WeeklyMoment(now, zone)
	if due.After(now) {
		due = time.Date(due.Year(), due.Month(), due.Day()-7, WeeklySummaryHour, 0, 0, 0, zone)
	}
	to := time.Date(due.Year(), due.Month(), due.Day(), 0, 0, 0, 0, zone)
	return Week{From: time.Date(to.Year(), to.Month(), to.Day()-7, 0, 0, 0, 0, zone), To: to}, due
}

// ISO is the week's ISO 8601 name in its own zone, such as 2026-W46.
func (w Week) ISO() string {
	year, week := w.From.ISOWeek()
	return fmt.Sprintf("%04d-W%02d", year, week)
}

// Monday is the date the week begins on, as 00:00 UTC of that day, which is
// how a weekly summary's email names it whatever zone the centre is in.
func (w Week) Monday() time.Time {
	return time.Date(w.From.Year(), w.From.Month(), w.From.Day(), 0, 0, 0, 0, time.UTC)
}

// DueEmailKey is the outbox key of a student's reminder lead before their
// close. It names the close, as DueSoonKey does, so a close that moves earns
// its reminders again.
func DueEmailKey(assignmentID, studentID string, lead Lead, closes time.Time) string {
	return "email:due:" + assignmentID + ":" + studentID + ":" + string(lead) + ":" + unix(closes)
}

// ClosingEmailKey is the outbox key of a teacher's warning that an
// assignment closes within the hour, naming the close.
func ClosingEmailKey(assignmentID, teacherID string, closes time.Time) string {
	return "email:closing:" + assignmentID + ":" + teacherID + ":" + unix(closes)
}

// ResultEmailKey is the outbox key of one attempt's result email, whichever
// sends it: grading that shows the score at once (PlanEmail) or the close
// that releases it, so a result is emailed once.
func ResultEmailKey(attemptID string) string {
	return "email:ready:" + attemptID
}

// WeeklyEmailKey is the outbox key of a teacher's summary of week.
func WeeklyEmailKey(teacherID string, week Week) string {
	return "email:weekly:" + teacherID + ":" + week.ISO()
}

// WeeklyKey is the dedupe key of a teacher's in-app summary of week.
func WeeklyKey(week Week) string {
	return "weekly:" + week.ISO()
}

// WeeklyFigures is a teacher's figures for one week on their active classes:
// papers handed in, of those the ones with an answer waiting for a mark,
// attempts flagged, and the names of the classes they came from, at most
// five: the fields the contract gives summary.weekly.
type WeeklyFigures struct {
	Week       Week
	Count      int
	ToGrade    int
	Flagged    int
	ClassNames []string
}

// MaxSummaryClasses is the most class names a weekly summary carries.
const MaxSummaryClasses = 5

// Active is whether the week had anything to tell: a teacher with no paper
// and no flag gets no summary.
func (s WeeklyFigures) Active() bool {
	return s.Count > 0 || s.Flagged > 0
}

// Notice is the summary as the teacher's in-app item.
func (s WeeklyFigures) Notice(teacherID string) Notice {
	return Notice{
		UserID: teacherID, Kind: SummaryWeekly,
		Params:    Weekly{Count: s.Count, ToGrade: s.ToGrade, Flagged: s.Flagged, ClassNames: s.ClassNames},
		Target:    &Target{Route: RouteDashboard},
		DedupeKey: WeeklyKey(s.Week), Merge: Replace,
	}
}

// Email is the summary as the teacher's email.
func (s WeeklyFigures) Email(teacherID string) Email {
	event := EventSummaryWeekly
	summary := s
	return Email{UserID: teacherID, Template: EmailWeeklySummary, Switch: &event, Summary: &summary, DedupeKey: WeeklyEmailKey(teacherID, s.Week)}
}

// DueEmail is a student's reminder, lead before their close, as an email.
func DueEmail(assignmentID, studentID string, lead Lead, closes time.Time) Email {
	event := EventAssignmentDueSoon
	return Email{UserID: studentID, Template: EmailAssignmentDueSoon, Switch: &event,
		IDs: EmailIDs{AssignmentID: assignmentID, Lead: lead}, DedupeKey: DueEmailKey(assignmentID, studentID, lead, closes)}
}

// ClosingEmail is a teacher's warning that an assignment closes within the
// hour, as an email.
func ClosingEmail(assignmentID, teacherID string, closes time.Time) Email {
	event := EventAssignmentClosing
	return Email{UserID: teacherID, Template: EmailAssignmentClosing, Switch: &event,
		IDs: EmailIDs{AssignmentID: assignmentID}, DedupeKey: ClosingEmailKey(assignmentID, teacherID, closes)}
}

// ReleasedEmail is a student's result that a close released, as an email.
func ReleasedEmail(attemptID, studentID string) Email {
	event := EventResultReady
	return Email{UserID: studentID, Template: EmailResultReady, Switch: &event,
		IDs: EmailIDs{AttemptID: attemptID}, DedupeKey: ResultEmailKey(attemptID)}
}
