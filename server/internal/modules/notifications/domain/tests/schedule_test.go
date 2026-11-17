package domain_test

import (
	"strconv"
	"testing"
	"time"

	"quizzivy/internal/modules/notifications/domain"
)

func zoneOf(t *testing.T, name string) *time.Location {
	t.Helper()
	zone, ok := domain.Zone(name)
	if !ok {
		t.Fatalf("zone %s does not load", name)
	}
	return zone
}

func TestTheWeeklySummaryFallsDueAtSevenOnMondayInTheTeachersOwnZone(t *testing.T) {
	tokyo := zoneOf(t, "Asia/Tokyo")
	due := time.Date(2026, 11, 15, 22, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		name string
		now  time.Time
		next time.Time
	}{
		{"a minute before seven", due.Add(-time.Minute), due},
		{"at seven", due, due.Add(7 * 24 * time.Hour)},
		{"on the Sunday before, UTC", time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC), due},
		{"on the Wednesday after", time.Date(2026, 11, 18, 3, 0, 0, 0, time.UTC), due.Add(7 * 24 * time.Hour)},
	} {
		if got := domain.NextWeekly(c.now, tokyo); !got.Equal(c.next) {
			t.Errorf("%s: next summary at %s, want %s", c.name, got, c.next)
		}
	}
	if got := domain.NextWeekly(due.Add(-time.Minute), time.UTC); !got.Equal(time.Date(2026, 11, 16, 7, 0, 0, 0, time.UTC)) {
		t.Errorf("in UTC the same moment waits for %s, want Monday 07:00 UTC", got)
	}
}

func TestLastWeekIsMondayToMondayInTheZoneAndNamedByItsISOWeek(t *testing.T) {
	tokyo := zoneOf(t, "Asia/Tokyo")
	week, due := domain.LastWeek(time.Date(2026, 11, 15, 22, 0, 0, 0, time.UTC), tokyo)
	if !due.Equal(time.Date(2026, 11, 15, 22, 0, 0, 0, time.UTC)) {
		t.Errorf("due at %s, want Monday 07:00 in Tokyo", due)
	}
	if !week.From.Equal(time.Date(2026, 11, 8, 15, 0, 0, 0, time.UTC)) || !week.To.Equal(time.Date(2026, 11, 15, 15, 0, 0, 0, time.UTC)) {
		t.Errorf("week %s to %s, want Monday 9 to Monday 16 November at midnight in Tokyo", week.From, week.To)
	}
	if week.ISO() != "2026-W46" {
		t.Errorf("week named %s, want 2026-W46", week.ISO())
	}
	if !week.Monday().Equal(time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("the email names the week by %s, want 9 November whatever the centre's zone", week.Monday())
	}
	before, earlier := domain.LastWeek(time.Date(2026, 11, 15, 21, 59, 0, 0, time.UTC), tokyo)
	if before.ISO() != "2026-W45" || !earlier.Equal(due.Add(-7*24*time.Hour)) {
		t.Errorf("before seven on Monday the latest summary due is %s's at %s, want 2026-W45's a week earlier", before.ISO(), earlier)
	}
	turn, _ := domain.LastWeek(time.Date(2027, 1, 4, 7, 30, 0, 0, time.UTC), time.UTC)
	if turn.ISO() != "2026-W53" {
		t.Errorf("the week ending on 4 January 2027 is %s, want 2026-W53", turn.ISO())
	}
}

func TestAZoneThatNamesNoneOrCannotLoadIsRefused(t *testing.T) {
	for _, name := range []string{"", "Local", "Mars/Olympus_Mons"} {
		if _, ok := domain.Zone(name); ok {
			t.Errorf("zone %q loaded", name)
		}
	}
}

func TestTheNextMomentIsTheEarliestAfterNowOfTheEmailsLeftOn(t *testing.T) {
	now := time.Date(2026, 11, 17, 3, 0, 0, 0, time.UTC)
	u := domain.Upcoming{
		DueSoon: now.Add(5 * time.Hour), Closing: now.Add(2 * time.Hour), Release: now.Add(3 * time.Hour),
	}
	if got, ok := u.Next(now, nil); !ok || !got.Equal(now.Add(2*time.Hour)) {
		t.Errorf("next %s %v, want the closing warning in two hours", got, ok)
	}
	noClosing := func(event string) bool { return event != "assignment.closing" }
	if got, _ := u.Next(now, noClosing); !got.Equal(now.Add(3 * time.Hour)) {
		t.Errorf("with the closing email off, next %s, want the release in three hours", got)
	}
	none := func(string) bool { return false }
	if got, ok := u.Next(now, none); ok {
		t.Errorf("with every email off something is due at %s", got)
	}
	u.Zones = []*time.Location{time.UTC}
	if got, ok := u.Next(now, none); !ok || !got.Equal(time.Date(2026, 11, 23, 7, 0, 0, 0, time.UTC)) {
		t.Errorf("a teacher's weekly summary is due at %s %v, want next Monday 07:00 UTC", got, ok)
	}
	if _, ok := (domain.Upcoming{}).Next(now, nil); ok {
		t.Error("with nothing open and no summary on, something is due")
	}
	if _, ok := (domain.Upcoming{DueSoon: now}).Next(now, nil); ok {
		t.Error("a moment that is now is due next, but the run at now has handled it")
	}
}

func TestTheScheduledEmailsAreKeyedAsThePlanNamesThemAndByTheClose(t *testing.T) {
	closes := time.Date(2026, 11, 20, 14, 30, 0, 0, time.UTC)
	due := domain.DueEmail(mailAssignment, mailStudent, domain.LeadHour, closes)
	if due.DedupeKey != "email:due:"+mailAssignment+":"+mailStudent+":1h:"+unixOf(closes) || due.IDs.Lead != domain.LeadHour ||
		due.Template != domain.EmailAssignmentDueSoon || *due.Switch != domain.EventAssignmentDueSoon {
		t.Errorf("due-soon email %+v", due)
	}
	if moved := domain.DueEmail(mailAssignment, mailStudent, domain.LeadHour, closes.Add(time.Hour)); moved.DedupeKey == due.DedupeKey {
		t.Error("a close that moved keeps its reminder's key, so the new close is never reminded")
	}
	closing := domain.ClosingEmail(mailAssignment, mailReader, closes)
	if closing.DedupeKey != "email:closing:"+mailAssignment+":"+mailReader+":"+unixOf(closes) || *closing.Switch != domain.EventAssignmentClosing {
		t.Errorf("closing email %+v", closing)
	}
	released := domain.ReleasedEmail(mailAttempt, mailStudent)
	graded, ok := domain.PlanEmail(domain.Notice{UserID: mailStudent, Kind: domain.ResultReady, Params: domain.Ready{Title: "Đề"},
		Target: &domain.Target{Route: domain.RouteResult, AssignmentID: mailAssignment, AttemptID: mailAttempt}, DedupeKey: domain.ReadyKey(mailAttempt)}, mailNow)
	if !ok || released.DedupeKey != graded.DedupeKey || released.DedupeKey != domain.ResultEmailKey(mailAttempt) {
		t.Errorf("a released result is keyed %q and a graded one %q: one paper could be emailed twice", released.DedupeKey, graded.DedupeKey)
	}
	week, _ := domain.LastWeek(mailNow, time.UTC)
	if got := domain.WeeklyEmailKey(mailReader, week); got != "email:weekly:"+mailReader+":2026-W46" {
		t.Errorf("weekly key %s", got)
	}
}

func unixOf(t time.Time) string { return strconv.FormatInt(t.Unix(), 10) }

func TestAWeeklySummaryIsWrittenOnlyWithActivityAndValidatesAsAnItem(t *testing.T) {
	week, _ := domain.LastWeek(mailNow, time.UTC)
	quiet := domain.WeeklyFigures{Week: week, ClassNames: []string{"Lớp 10A"}}
	if quiet.Active() {
		t.Error("a week with no paper and no flag is active")
	}
	flagged := domain.WeeklyFigures{Week: week, Flagged: 1, ClassNames: []string{"Lớp 10A"}}
	if !flagged.Active() {
		t.Error("a week with a flag is not active")
	}
	busy := domain.WeeklyFigures{Week: week, Count: 12, ToGrade: 3, Flagged: 1, ClassNames: []string{"Lớp 10A", "Lớp 11B"}}
	n := busy.Notice(mailReader)
	if err := n.Validate(); err != nil {
		t.Fatalf("the summary's item is refused: %v", err)
	}
	if n.Target.Route != domain.RouteDashboard || n.DedupeKey != "weekly:2026-W46" || n.Kind != domain.SummaryWeekly {
		t.Errorf("item %+v", n)
	}
	e := busy.Email(mailReader)
	if e.Template != domain.EmailWeeklySummary || *e.Switch != domain.EventSummaryWeekly || e.Summary.Count != 12 {
		t.Errorf("email %+v", e)
	}
}
