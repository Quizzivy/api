//go:build integration

package repositories_test

import (
	"context"
	"encoding/json"
	"slices"
	"sync"
	"testing"
	"time"

	"quizzivy/internal/modules/notifications/application"
	"quizzivy/internal/modules/notifications/application/command"
	"quizzivy/internal/modules/notifications/application/query"
	"quizzivy/internal/modules/notifications/domain"
	"quizzivy/internal/modules/notifications/repositories"
	"quizzivy/internal/platform/db"
	"quizzivy/internal/platform/email"
)

// far is a Wednesday no other row of the test database reaches, so that the
// scheduler, which reads every account at once, finds only a test's own.
var far = time.Date(2041, 3, 13, 9, 0, 0, 0, time.UTC)

func newFarWorld(t *testing.T) *dueWorld {
	t.Helper()
	w := newDueWorld(t)
	w.now = far
	return w
}

func (w *dueWorld) scheduler(sends func(string) bool) *application.Application {
	return application.New(repositories.NewPostgres(db.NewContext(w.tx))).
		WithSchedule(repositories.NewSchedule(db.NewContext(w.tx), email.NewOutbox(nil), nil), sends)
}

func (w *dueWorld) runDue(app *application.Application, at time.Time) command.Scheduled {
	w.t.Helper()
	done, err := app.Commands.SendDueEmails.Handle(context.Background(), command.SendDueEmails{Now: at})
	if err != nil {
		w.t.Fatalf("due emails at %s: %v", at, err)
	}
	return done
}

func (w *dueWorld) runWeekly(app *application.Application, at time.Time) command.Scheduled {
	w.t.Helper()
	done, err := app.Commands.SendWeeklySummary.Handle(context.Background(), command.SendWeeklySummary{Now: at})
	if err != nil {
		w.t.Fatalf("weekly summary at %s: %v", at, err)
	}
	return done
}

func (w *dueWorld) upcoming(at time.Time) domain.Upcoming {
	w.t.Helper()
	u, err := repositories.NewSchedule(db.NewContext(w.tx), nil, nil).Upcoming(context.Background(), at, domain.WeeklyOn{Email: true})
	if err != nil {
		w.t.Fatal(err)
	}
	return u
}

func keysIn(rows []queued) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.key
	}
	slices.Sort(out)
	return out
}

func sameMoment(t *testing.T, what string, got, want time.Time) {
	t.Helper()
	if !got.Equal(want) {
		t.Errorf("%s at %s, want %s", what, got, want)
	}
}

// TestTheNextMomentsFollowOverridesEarlyClosesAndExtensions is the next-due
// computation at its boundaries: a close a day away less what has passed,
// an override that moves one student's close later, an early close that
// brings every moment forward, an extension that moves them back and leaves
// an override it overtook behind, a draft that has none, and the zone of a
// teacher whose weekly summary is on.
func TestTheNextMomentsFollowOverridesEarlyClosesAndExtensionsLeaveThem(t *testing.T) {
	w := newFarWorld(t)
	a := w.assignment(spec{opens: far.Add(-72 * time.Hour), closes: far.Add(10 * time.Hour), release: "after_close"})
	w.assignment(spec{opens: far.Add(-72 * time.Hour), closes: far.Add(time.Hour), draft: true})

	u := w.upcoming(far)
	sameMoment(t, "the reminder", u.DueSoon, far.Add(9*time.Hour))
	sameMoment(t, "the closing warning", u.Closing, far.Add(9*time.Hour))
	sameMoment(t, "the release", u.Release, far.Add(10*time.Hour))

	later := far.Add(30 * time.Hour)
	w.override(a, w.student, &later, 0)
	u = w.upcoming(far)
	sameMoment(t, "with an override a day and six hours away, the reminder", u.DueSoon, far.Add(6*time.Hour))
	sameMoment(t, "with an override, the closing warning", u.Closing, far.Add(9*time.Hour))
	sameMoment(t, "with an override, the release", u.Release, far.Add(10*time.Hour))
	if next, _ := u.Next(far, nil); !next.Equal(far.Add(6 * time.Hour)) {
		t.Errorf("the scheduler next wakes at %s, want the override's reminder", next)
	}

	w.exec(`UPDATE app.assignments SET closed_at = $2 WHERE id = $1`, a, far.Add(2*time.Hour))
	u = w.upcoming(far)
	sameMoment(t, "closed early, the reminder", u.DueSoon, far.Add(time.Hour))
	sameMoment(t, "closed early, the closing warning", u.Closing, far.Add(time.Hour))
	sameMoment(t, "closed early, the release", u.Release, far.Add(2*time.Hour))

	w.exec(`UPDATE app.assignments SET closed_at = NULL, closes_at = $2 WHERE id = $1`, a, far.Add(50*time.Hour))
	u = w.upcoming(far)
	sameMoment(t, "extended past the override, the reminder", u.DueSoon, far.Add(26*time.Hour))
	sameMoment(t, "extended, the closing warning", u.Closing, far.Add(49*time.Hour))
	sameMoment(t, "extended, the release", u.Release, far.Add(50*time.Hour))

	saigon, _ := domain.Zone(domain.DefaultZone)
	if !slices.ContainsFunc(u.Zones, func(z *time.Location) bool { return z.String() == saigon.String() }) {
		t.Errorf("zones %v: the world's teacher, whose summary is on by default, is not counted", u.Zones)
	}
}

// TestNothingOpenLeavesNoMomentButTheTeachersMondays: with every
// assignment closed there is nothing to wake for but the teachers' Mondays,
// so the scheduler wakes only for its six-hour check (NFR-A10, PR-7).
func TestNothingOpenLeavesNoMomentButTheTeachersMondays(t *testing.T) {
	w := newFarWorld(t)
	w.assignment(spec{opens: far.Add(-72 * time.Hour), closes: far.Add(-time.Hour), release: "after_close"})
	u := w.upcoming(far)
	if !u.DueSoon.IsZero() || !u.Closing.IsZero() || !u.Release.IsZero() {
		t.Errorf("a closed assignment still has moments: %+v", u)
	}
}

// TestTheRemindersFallADayAndAnHourBeforeEachStudentsOwnClose runs the
// scheduler as T-R4.10b's rule reads due-soon items: a student who has not
// handed in is reminded a day before their own close, a student whose
// override is later waits for theirs, a student who handed in or has no
// attempt left is not, an assignment that opened less than a day before
// its close gets the hour's reminder alone, and a second run enqueues
// nothing.
func TestTheRemindersFallADayAndAnHourBeforeEachStudentsOwnClose(t *testing.T) {
	w := newFarWorld(t)
	closes := far.Add(23 * time.Hour)
	a := w.assignment(spec{opens: far.Add(-72 * time.Hour), closes: closes})
	late, done, spent := w.user("student"), w.user("student"), w.user("student")
	for _, s := range []string{late, done, spent} {
		w.join(w.class, s)
	}
	lateClose := far.Add(30 * time.Hour)
	w.override(a, late, &lateClose, 0)
	w.attempt(a, done, "submitted", far.Add(-time.Hour))
	w.attempt(a, spent, "in_progress", far.Add(2*time.Hour))
	short := w.assignment(spec{title: "Đề ngắn", opens: far.Add(-time.Hour), closes: far.Add(10 * time.Hour)})

	app := w.scheduler(nil)
	if got := w.runDue(app, far); got.Emails != 1 {
		t.Errorf("the first run wrote %d emails, want the day's reminder alone", got.Emails)
	}
	want := []string{domain.DueEmailKey(a, w.student, domain.LeadDay, closes)}
	if got := keysIn(w.outbox(w.student)); !slices.Equal(got, want) {
		t.Errorf("the student's queue %v, want %v", got, want)
	}
	for _, s := range []string{late, done, spent} {
		if got := w.outbox(s); len(got) != 0 {
			t.Errorf("a student with a later close, a paper handed in or no attempt left has %+v", got)
		}
	}
	var params map[string]any
	if err := json.Unmarshal([]byte(w.outbox(w.student)[0].params), &params); err != nil {
		t.Fatal(err)
	}
	if params["assignmentId"] != a || params["lead"] != string(domain.LeadDay) || len(params) != 2 {
		t.Errorf("the reminder stores %v, want its assignment and lead alone", params)
	}
	if got := w.runDue(app, far); got.Emails != 0 || len(w.outbox(w.student)) != 1 {
		t.Errorf("a second run wrote %d emails", got.Emails)
	}

	hour := closes.Add(-time.Hour)
	w.runDue(app, hour)
	want = append(want, domain.DueEmailKey(a, w.student, domain.LeadHour, closes))
	slices.Sort(want)
	if got := keysIn(w.outbox(w.student)); !slices.Equal(got, want) {
		t.Errorf("at the hour the student's queue %v, want %v", got, want)
	}
	if got := keysIn(w.outbox(late)); !slices.Equal(got, []string{domain.DueEmailKey(a, late, domain.LeadDay, lateClose)}) {
		t.Errorf("a day before the override's close the late student has %v", got)
	}
	if got := keysIn(w.outbox(w.student)); slices.ContainsFunc(got, func(k string) bool {
		return k == domain.DueEmailKey(short, w.student, domain.LeadDay, far.Add(10*time.Hour))
	}) {
		t.Error("an assignment that opened less than a day before its close sent the day's reminder")
	}
	w.runDue(app, far.Add(9*time.Hour))
	if got := keysIn(w.outbox(w.student)); !slices.Contains(got, domain.DueEmailKey(short, w.student, domain.LeadHour, far.Add(10*time.Hour))) {
		t.Errorf("the short assignment's hour reminder is missing from %v", got)
	}
}

// TestARestartInsideTheWindowCatchesUpOnce: a run reads every moment of
// the last 26 hours, so a scheduler that was down at a close still sends
// what it released, once, and one that was down longer sends nothing stale.
func TestARestartInsideTheWindowCatchesUpOnce(t *testing.T) {
	w := newFarWorld(t)
	recent := w.assignment(spec{opens: far.Add(-72 * time.Hour), closes: far.Add(-25 * time.Hour), release: "after_close"})
	stale := w.assignment(spec{title: "Đề cũ", opens: far.Add(-72 * time.Hour), closes: far.Add(-27 * time.Hour), release: "after_close"})
	caught := w.attempt(recent, w.student, "graded", far.Add(-26*time.Hour))
	w.attempt(stale, w.student, "graded", far.Add(-28*time.Hour))

	app := w.scheduler(nil)
	w.runDue(app, far)
	w.runDue(app, far.Add(time.Minute))
	if got := keysIn(w.outbox(w.student)); !slices.Equal(got, []string{domain.ResultEmailKey(caught)}) {
		t.Errorf("after a restart the queue is %v, want the result released 25 hours ago, once", got)
	}
}

// TestNothingIsSentForAClosedOrHandedInAssignment: an early close stops
// every reminder and warning, a teacher is warned only while a student they
// reach has not handed in and only with that email turned on, and a result
// is released only to a graded paper whose score the policy shows.
func TestNothingIsSentForAClosedOrHandedInAssignment(t *testing.T) {
	w := newFarWorld(t)
	w.emailSwitch(w.creator, domain.EventAssignmentClosing, true, true)
	closedEarly := w.assignment(spec{opens: far.Add(-72 * time.Hour), closes: far.Add(5 * time.Hour)})
	w.exec(`UPDATE app.assignments SET closed_at = $2 WHERE id = $1`, closedEarly, far.Add(-10*time.Minute))
	allIn := w.assignment(spec{title: "Đề nộp đủ", opens: far.Add(-72 * time.Hour), closes: far.Add(30 * time.Minute)})
	w.attempt(allIn, w.student, "submitted", far.Add(-time.Hour))
	open := w.assignment(spec{title: "Đề còn mở", opens: far.Add(-72 * time.Hour), closes: far.Add(40 * time.Minute)})
	quiet := w.user("teacher")
	quietClass := w.classOf(quiet)
	w.join(quietClass, w.student)
	w.exec(`INSERT INTO app.assignment_classes (assignment_id, class_id) VALUES ($1, $2)`, open, quietClass)

	release := w.assignment(spec{title: "Đề công bố", opens: far.Add(-72 * time.Hour), closes: far.Add(-time.Hour), release: "after_close"})
	waiting := w.attempt(release, w.student, "submitted", far.Add(-2*time.Hour))
	w.pending(waiting)
	hidden := w.assignment(spec{title: "Đề ẩn điểm", opens: far.Add(-72 * time.Hour), closes: far.Add(-time.Hour), release: "after_close", hideScore: true})
	w.attempt(hidden, w.student, "graded", far.Add(-2*time.Hour))
	onSubmit := w.assignment(spec{title: "Đề hiện ngay", opens: far.Add(-72 * time.Hour), closes: far.Add(-time.Hour)})
	w.attempt(onSubmit, w.student, "graded", far.Add(-2*time.Hour))

	w.runDue(w.scheduler(nil), far)
	if got := keysIn(w.outbox(w.creator)); !slices.Equal(got, []string{domain.ClosingEmailKey(open, w.creator, far.Add(40*time.Minute))}) {
		t.Errorf("the teacher's queue %v, want the warning for the assignment still waiting on a student", got)
	}
	if got := w.outbox(quiet); len(got) != 0 {
		t.Errorf("a teacher whose closing email is off by default has %+v", got)
	}
	if got := keysIn(w.outbox(w.student)); !slices.Equal(got, []string{domain.DueEmailKey(open, w.student, domain.LeadHour, far.Add(40*time.Minute))}) {
		t.Errorf("the student's queue %v, want the hour's reminder of the open assignment alone", got)
	}
}

// TestAReleasedResultIsEmailedOnceWhicheverPathSendsIt: a paper graded
// after the close has its email from grading, and the close's release does
// not send a second.
func TestAReleasedResultIsEmailedOnceWhicheverPathSendsIt(t *testing.T) {
	w := newFarWorld(t)
	a := w.assignment(spec{opens: far.Add(-72 * time.Hour), closes: far.Add(-time.Hour), release: "after_close"})
	paper := w.attempt(a, w.student, "graded", far.Add(-2*time.Hour))
	w.mail(readyNotice(w.student, paper, a), far)
	w.runDue(w.scheduler(nil), far)
	if got := keysIn(w.outbox(w.student)); !slices.Equal(got, []string{domain.ResultEmailKey(paper)}) {
		t.Errorf("the student's queue %v, want one result email", got)
	}
}

// TestTheKillSwitchKeepsAScheduledEmailOutOfTheOutbox: NOTIFY_EMAIL_EVENTS
// turns a scheduled email off as it does Notify's.
func TestTheKillSwitchKeepsAScheduledEmailOutOfTheOutbox(t *testing.T) {
	w := newFarWorld(t)
	w.assignment(spec{opens: far.Add(-72 * time.Hour), closes: far.Add(30 * time.Minute)})
	done := w.runDue(w.scheduler(func(event string) bool { return event != "assignment.due_soon" }), far)
	if got := w.outbox(w.student); len(got) != 0 || done.Skipped != 1 {
		t.Errorf("with assignment.due_soon off the student has %+v (%+v)", got, done)
	}
}

// TestTheWeeklySummaryInATokyoTeachersWeek is the summary in a UTC+9 zone:
// it falls due at 07:00 on Monday in Tokyo, counts the papers of the week
// that ended at midnight there on the teacher's active classes, writes the
// email by default and the in-app item only once its switch is on, and is
// written once.
func TestTheWeeklySummaryInATokyoTeachersWeek(t *testing.T) {
	w := newFarWorld(t)
	teacher := w.user("teacher")
	w.exec(`UPDATE app.users SET time_zone = 'Asia/Tokyo' WHERE id = $1`, teacher)
	active := w.id(`INSERT INTO app.classes (name, teacher_id) VALUES ('IELTS 7.0', $1) RETURNING id::text`, teacher)
	archived := w.id(`INSERT INTO app.classes (name, teacher_id, archived_at) VALUES ('Lớp cũ', $1, $2) RETURNING id::text`, teacher, far.Add(-30*24*time.Hour))
	students := []string{w.user("student"), w.user("student"), w.user("student"), w.user("student"), w.user("student")}
	for _, s := range students[:4] {
		w.join(active, s)
	}
	w.join(archived, students[4])
	seven := time.Date(2041, 3, 10, 22, 0, 0, 0, time.UTC)
	a := w.assignment(spec{creator: teacher, classes: []string{active}, opens: seven.Add(-10 * 24 * time.Hour), closes: seven.Add(10 * 24 * time.Hour)})
	old := w.assignment(spec{creator: teacher, classes: []string{archived}, opens: seven.Add(-10 * 24 * time.Hour), closes: seven.Add(10 * 24 * time.Hour)})
	sundayNight := time.Date(2041, 3, 10, 14, 30, 0, 0, time.UTC)
	mondayMidnight := time.Date(2041, 3, 10, 15, 30, 0, 0, time.UTC)
	w.attempt(a, students[0], "graded", sundayNight)
	w.attempt(a, students[1], "submitted", mondayMidnight)
	flagged := w.attempt(a, students[2], "graded", sundayNight.Add(-24*time.Hour))
	w.exec(`UPDATE app.attempts SET flagged = true WHERE id = $1`, flagged)
	w.pending(w.attempt(a, students[3], "submitted", sundayNight.Add(-48*time.Hour)))
	w.attempt(old, students[4], "graded", sundayNight)

	app := w.scheduler(nil)
	if got := w.runWeekly(app, seven.Add(-time.Minute)); got.Emails+got.InApp != 0 {
		t.Errorf("a minute before seven in Tokyo the run wrote %+v", got)
	}
	if got := w.runWeekly(app, seven); got.Emails != 1 || got.InApp != 0 {
		t.Errorf("at seven in Tokyo the run wrote %+v, want the email alone: the item is off by default", got)
	}
	queue := w.outbox(teacher)
	if len(queue) != 1 || queue[0].key != "email:weekly:"+teacher+":2041-W10" {
		t.Fatalf("the teacher's queue %+v", queue)
	}
	var params email.WeeklySummaryParams
	if err := json.Unmarshal([]byte(queue[0].params), &params); err != nil {
		t.Fatal(err)
	}
	if params.Count != 3 || params.ToGrade != 1 || params.Flagged != 1 || !slices.Equal(params.ClassNames, []string{"IELTS 7.0"}) ||
		!params.WeekOf.Equal(time.Date(2041, 3, 4, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("the summary says %+v, want three papers of the active class, one to grade, one flagged, the week of Monday 4 March", params)
	}
	if got := w.outbox(w.creator); len(got) != 0 {
		t.Errorf("a teacher with no activity has %+v", got)
	}

	w.prefer(teacher, domain.EventSummaryWeekly, true)
	if got := w.runWeekly(app, seven.Add(time.Hour)); got.Emails != 0 || got.InApp != 1 {
		t.Errorf("with the in-app switch turned on the run wrote %+v, want the item alone", got)
	}
	item := theRowIn(t, w, teacher)
	if item.kind != string(domain.SummaryWeekly) || item.target["route"] != string(domain.RouteDashboard) ||
		item.params["count"] != float64(3) || item.params["flagged"] != float64(1) || item.params["toGrade"] != float64(1) {
		t.Errorf("the item %+v", item)
	}
	if got := w.runWeekly(app, seven.Add(2*time.Hour)); got.Emails+got.InApp != 0 {
		t.Errorf("a third run wrote %+v", got)
	}
	if got := w.runWeekly(app, seven.Add(domain.ScheduleLookback+time.Minute)); got.Emails+got.InApp != 0 {
		t.Errorf("past the window the run wrote %+v", got)
	}
}

func theRowIn(t *testing.T, w *dueWorld, userID string) stored {
	t.Helper()
	var s stored
	var params, target []byte
	if err := w.tx.QueryRow(context.Background(), `SELECT kind, params, target FROM app.notifications WHERE user_id = $1::uuid`, userID).
		Scan(&s.kind, &params, &target); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(params, &s.params); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(target, &s.target); err != nil {
		t.Fatal(err)
	}
	return s
}

// TestTwoSchedulersUnderTheLockEnqueueOneSet runs two machines' schedulers
// at once over committed rows: whichever holds the lock writes the set, the
// other is busy or finds it written, and a run while another transaction
// holds the lock writes nothing.
func TestTwoSchedulersUnderTheLockEnqueueOneSet(t *testing.T) {
	pool := newPool(t)
	ctx := context.Background()
	teacher := newUser(t, pool, "teacher")
	students := []string{newUser(t, pool, "student"), newUser(t, pool, "student"), newUser(t, pool, "student")}
	var class, test, assignment string
	t.Cleanup(func() {
		for _, sql := range []string{
			`DELETE FROM app.email_outbox WHERE to_user_id = ANY($1::uuid[])`,
			`DELETE FROM app.notifications WHERE user_id = ANY($1::uuid[])`,
		} {
			if _, err := pool.Exec(ctx, sql, append([]string{teacher}, students...)); err != nil {
				t.Errorf("cleanup: %v", err)
			}
		}
		if assignment != "" {
			for _, sql := range []string{
				`DELETE FROM app.assignment_classes WHERE assignment_id = $1`,
				`DELETE FROM app.assignments WHERE id = $1`,
			} {
				if _, err := pool.Exec(ctx, sql, assignment); err != nil {
					t.Errorf("cleanup: %v", err)
				}
			}
		}
		if test != "" {
			for _, sql := range []string{`DELETE FROM app.test_versions WHERE test_id = $1`, `DELETE FROM app.tests WHERE id = $1`} {
				if _, err := pool.Exec(ctx, sql, test); err != nil {
					t.Errorf("cleanup: %v", err)
				}
			}
		}
		if class != "" {
			if _, err := pool.Exec(ctx, `DELETE FROM app.classes WHERE id = $1`, class); err != nil {
				t.Errorf("cleanup: %v", err)
			}
		}
	})
	scan := func(sql string, args ...any) string {
		t.Helper()
		var id string
		if err := pool.QueryRow(ctx, sql, args...).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	class = scan(`INSERT INTO app.classes (name, teacher_id) VALUES ('Lớp khóa', $1) RETURNING id::text`, teacher)
	for _, s := range students {
		if _, err := pool.Exec(ctx, `INSERT INTO app.class_members (class_id, user_id, joined_via, added_by) VALUES ($1, $2, 'admin', $2)`, class, s); err != nil {
			t.Fatal(err)
		}
	}
	test = scan(`INSERT INTO app.tests (title, status, current_version, created_by, owner_id) VALUES ('Đề khóa', 'published', 1, $1, $1) RETURNING id::text`, teacher)
	version := scan(`INSERT INTO app.test_versions (test_id, version, total_points, published_by) VALUES ($1, 1, 10, $2) RETURNING id::text`, test, teacher)
	closes := far.Add(30 * time.Minute)
	assignment = scan(`INSERT INTO app.assignments (test_id, test_version_id, opens_at, closes_at, duration_minutes, created_by, published_at)
		VALUES ($1, $2, $3, $4, 45, $5, $6) RETURNING id::text`, test, version, far.Add(-72*time.Hour), closes, teacher, far.Add(-73*time.Hour))
	if _, err := pool.Exec(ctx, `INSERT INTO app.assignment_classes (assignment_id, class_id) VALUES ($1, $2)`, assignment, class); err != nil {
		t.Fatal(err)
	}

	holder, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := holder.Exec(ctx, `SELECT pg_advisory_xact_lock(73819, 42)`); err != nil {
		t.Fatal(err)
	}
	machine := func() *application.Application {
		return application.New(repositories.NewPostgres(db.NewContext(pool))).
			WithSchedule(repositories.NewSchedule(db.NewContext(pool), email.NewOutbox(nil), nil), nil)
	}
	held, err := machine().Commands.SendDueEmails.Handle(ctx, command.SendDueEmails{Now: far})
	if err != nil || !held.Busy || held.Emails != 0 {
		t.Errorf("while another transaction holds the lock the run did %+v %v, want busy", held, err)
	}
	if err := holder.Rollback(ctx); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	results := make(chan command.Scheduled, 8)
	for range 2 {
		app := machine()
		wg.Go(func() {
			for range 4 {
				done, err := app.Commands.SendDueEmails.Handle(ctx, command.SendDueEmails{Now: far})
				if err != nil {
					t.Error(err)
					return
				}
				results <- done
			}
		})
	}
	wg.Wait()
	close(results)
	written := 0
	for done := range results {
		written += done.Emails
	}
	var rows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM app.email_outbox WHERE to_user_id = ANY($1::uuid[])`, students).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != len(students) || written != len(students) {
		t.Errorf("two schedulers wrote %d rows (%d reported), want one reminder for each of %d students", rows, written, len(students))
	}

	due, err := machine().Queries.NextDue.Handle(ctx, query.NextDue{Now: far.Add(-2 * time.Hour)})
	if err != nil || !due.Any || !due.At.Equal(closes.Add(-time.Hour)) {
		t.Errorf("two hours earlier the next moment is %+v %v, want the hour before the close", due, err)
	}
}
