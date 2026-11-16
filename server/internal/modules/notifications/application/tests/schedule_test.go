package application_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"quizzivy/internal/modules/notifications/application"
	"quizzivy/internal/modules/notifications/application/command"
	"quizzivy/internal/modules/notifications/application/query"
	"quizzivy/internal/modules/notifications/domain"
)

const (
	plannedStudent    = "0192a7c0-0000-7000-8000-0000000000b1"
	plannedTeacher    = "0192a7c0-0000-7000-8000-0000000000b2"
	plannedAssignment = "0192a7c0-0000-7000-8000-0000000000b3"
)

type fakeSchedule struct {
	busy      bool
	upcoming  domain.Upcoming
	weeklyOn  []domain.WeeklyOn
	due       []domain.Email
	teachers  []domain.WeeklyTeacher
	figures   domain.WeeklyFigures
	heldEmail bool
	heldItem  bool
	enqueued  []domain.Email
	published []domain.Notice
	summed    []domain.Week
}

func (f *fakeSchedule) Upcoming(_ context.Context, _ time.Time, weekly domain.WeeklyOn) (domain.Upcoming, error) {
	f.weeklyOn = append(f.weeklyOn, weekly)
	return f.upcoming, nil
}

func (f *fakeSchedule) Locked(_ context.Context, fn func(domain.ScheduledRun) error) (bool, error) {
	if f.busy {
		return false, nil
	}
	return true, fn(f)
}

func (f *fakeSchedule) DueEmails(context.Context, time.Time) ([]domain.Email, error) {
	return f.due, nil
}

func (f *fakeSchedule) WeeklyTeachers(_ context.Context, weekly domain.WeeklyOn) ([]domain.WeeklyTeacher, error) {
	f.weeklyOn = append(f.weeklyOn, weekly)
	return f.teachers, nil
}

func (f *fakeSchedule) WeeklyHeld(context.Context, string, domain.Week) (bool, bool, error) {
	return f.heldEmail, f.heldItem, nil
}

func (f *fakeSchedule) Summarise(_ context.Context, _ string, week domain.Week) (domain.WeeklyFigures, error) {
	f.summed = append(f.summed, week)
	figures := f.figures
	figures.Week = week
	return figures, nil
}

func (f *fakeSchedule) Enqueue(_ context.Context, e domain.Email) (bool, error) {
	f.enqueued = append(f.enqueued, e)
	return true, nil
}

func (f *fakeSchedule) Publish(_ context.Context, n domain.Notice) (bool, error) {
	f.published = append(f.published, n)
	return true, nil
}

func scheduled(t *testing.T, store *fakeSchedule, sends func(string) bool) *application.Application {
	t.Helper()
	return application.New(only(t)).WithSchedule(store, sends)
}

var mondayInTokyo = time.Date(2026, 11, 15, 22, 0, 0, 0, time.UTC)

func TestTheScheduledRunsRefuseWithoutTheirStore(t *testing.T) {
	app := application.New(only(t))
	if _, err := app.Commands.SendDueEmails.Handle(context.Background(), command.SendDueEmails{Now: mondayInTokyo}); !errors.Is(err, domain.ErrNoSchedule) {
		t.Errorf("due emails: %v, want ErrNoSchedule", err)
	}
	if _, err := app.Commands.SendWeeklySummary.Handle(context.Background(), command.SendWeeklySummary{Now: mondayInTokyo}); !errors.Is(err, domain.ErrNoSchedule) {
		t.Errorf("weekly summary: %v, want ErrNoSchedule", err)
	}
	if due, err := app.Queries.NextDue.Handle(context.Background(), query.NextDue{Now: mondayInTokyo}); err != nil || due.Any {
		t.Errorf("next due %+v %v, want nothing", due, err)
	}
}

func TestADueEmailTheKillSwitchNamesIsNotEnqueued(t *testing.T) {
	closes := mondayInTokyo.Add(time.Hour)
	store := &fakeSchedule{due: []domain.Email{
		domain.DueEmail(plannedAssignment, plannedStudent, domain.LeadHour, closes),
		domain.ClosingEmail(plannedAssignment, plannedTeacher, closes),
	}}
	done, err := scheduled(t, store, func(event string) bool { return event != "assignment.closing" }).
		Commands.SendDueEmails.Handle(context.Background(), command.SendDueEmails{Now: mondayInTokyo})
	if err != nil {
		t.Fatal(err)
	}
	if len(store.enqueued) != 1 || store.enqueued[0].Template != domain.EmailAssignmentDueSoon || done.Emails != 1 || done.Skipped != 1 {
		t.Errorf("enqueued %+v, done %+v: want the reminder alone", store.enqueued, done)
	}
}

func TestARunAnotherMachineHoldsWritesNothingAndSaysSo(t *testing.T) {
	store := &fakeSchedule{busy: true, due: []domain.Email{domain.ReleasedEmail(plannedAssignment, plannedStudent)},
		teachers: []domain.WeeklyTeacher{{ID: plannedTeacher, Zone: "Asia/Tokyo", Email: true}}}
	app := scheduled(t, store, nil)
	due, err := app.Commands.SendDueEmails.Handle(context.Background(), command.SendDueEmails{Now: mondayInTokyo})
	if err != nil || !due.Busy {
		t.Errorf("due %+v %v, want busy", due, err)
	}
	weekly, err := app.Commands.SendWeeklySummary.Handle(context.Background(), command.SendWeeklySummary{Now: mondayInTokyo})
	if err != nil || !weekly.Busy {
		t.Errorf("weekly %+v %v, want busy", weekly, err)
	}
	if len(store.enqueued)+len(store.published) != 0 {
		t.Errorf("a busy run wrote %+v %+v", store.enqueued, store.published)
	}
}

func TestTheWeeklySummaryGoesInEachChannelStillToWriteAndOnlyWithActivity(t *testing.T) {
	teacher := domain.WeeklyTeacher{ID: plannedTeacher, Zone: "Asia/Tokyo", Email: true, InApp: true}
	busy := domain.WeeklyFigures{Count: 4, ToGrade: 1, ClassNames: []string{"Lớp 10A"}}
	store := &fakeSchedule{teachers: []domain.WeeklyTeacher{teacher}, figures: busy}
	done, err := scheduled(t, store, nil).Commands.SendWeeklySummary.Handle(context.Background(), command.SendWeeklySummary{Now: mondayInTokyo})
	if err != nil {
		t.Fatal(err)
	}
	if done.Emails != 1 || done.InApp != 1 || len(store.summed) != 1 || store.summed[0].ISO() != "2026-W46" {
		t.Errorf("done %+v, weeks %+v", done, store.summed)
	}

	early := &fakeSchedule{teachers: []domain.WeeklyTeacher{teacher}, figures: busy}
	if _, err := scheduled(t, early, nil).Commands.SendWeeklySummary.Handle(context.Background(),
		command.SendWeeklySummary{Now: mondayInTokyo.Add(-time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if len(early.summed) != 0 {
		t.Errorf("a minute before seven in Tokyo a week was summed: %+v", early.summed)
	}

	late := &fakeSchedule{teachers: []domain.WeeklyTeacher{teacher}, figures: busy}
	if _, err := scheduled(t, late, nil).Commands.SendWeeklySummary.Handle(context.Background(),
		command.SendWeeklySummary{Now: mondayInTokyo.Add(domain.ScheduleLookback)}); err != nil {
		t.Fatal(err)
	}
	if len(late.summed) != 0 {
		t.Errorf("26 hours after seven the summary was still computed: %+v", late.summed)
	}

	held := &fakeSchedule{teachers: []domain.WeeklyTeacher{teacher}, figures: busy, heldEmail: true}
	if _, err := scheduled(t, held, nil).Commands.SendWeeklySummary.Handle(context.Background(), command.SendWeeklySummary{Now: mondayInTokyo}); err != nil {
		t.Fatal(err)
	}
	if len(held.enqueued) != 0 || len(held.published) != 1 {
		t.Errorf("with the email already queued: enqueued %d, published %d, want the item alone", len(held.enqueued), len(held.published))
	}

	quiet := &fakeSchedule{teachers: []domain.WeeklyTeacher{teacher}, figures: domain.WeeklyFigures{}}
	if _, err := scheduled(t, quiet, nil).Commands.SendWeeklySummary.Handle(context.Background(), command.SendWeeklySummary{Now: mondayInTokyo}); err != nil {
		t.Fatal(err)
	}
	if len(quiet.enqueued)+len(quiet.published) != 0 {
		t.Error("a teacher with no activity got a summary")
	}

	lost := &fakeSchedule{teachers: []domain.WeeklyTeacher{{ID: plannedTeacher, Zone: "Mars/Olympus_Mons", Email: true}}, figures: busy}
	done, err = scheduled(t, lost, nil).Commands.SendWeeklySummary.Handle(context.Background(), command.SendWeeklySummary{Now: mondayInTokyo})
	if err != nil || done.Skipped != 1 || len(lost.summed) != 0 {
		t.Errorf("a zone this host cannot load: done %+v %v", done, err)
	}
}

func TestTheWeeklySummarysEmailCountsOnlyWhileTheKillSwitchLeavesItOn(t *testing.T) {
	store := &fakeSchedule{}
	app := scheduled(t, store, func(event string) bool { return event != "summary.weekly" })
	if _, err := app.Commands.SendWeeklySummary.Handle(context.Background(), command.SendWeeklySummary{Now: mondayInTokyo}); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Queries.NextDue.Handle(context.Background(), query.NextDue{Now: mondayInTokyo}); err != nil {
		t.Fatal(err)
	}
	for _, on := range store.weeklyOn {
		if on.Email {
			t.Errorf("the summary's email counted while NOTIFY_EMAIL_EVENTS turned it off: %+v", store.weeklyOn)
		}
	}
	if len(store.weeklyOn) != 2 {
		t.Errorf("read the teachers %d times, want twice", len(store.weeklyOn))
	}
}
