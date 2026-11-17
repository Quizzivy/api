package application_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"quizzivy/internal/modules/assignments/application"
	"quizzivy/internal/modules/assignments/application/command"
	"quizzivy/internal/modules/assignments/domain"
)

type closingRepository struct {
	domain.Repository
	err error
}

func (r closingRepository) Create(context.Context, domain.Request, domain.WriteInput) (domain.Assignment, error) {
	return domain.Assignment{}, r.err
}

func (r closingRepository) Update(context.Context, domain.Request, domain.WriteInput) (domain.Assignment, error) {
	return domain.Assignment{}, r.err
}

func (r closingRepository) Reopen(context.Context, domain.Request, time.Time, string, time.Time) (domain.Assignment, error) {
	return domain.Assignment{}, r.err
}

func (r closingRepository) Extend(context.Context, domain.Request, int, bool, time.Time) (domain.Assignment, error) {
	return domain.Assignment{}, r.err
}

func (r closingRepository) SetOverrides(context.Context, domain.Request, domain.OverrideInput) ([]domain.StudentOverride, error) {
	return nil, r.err
}

func (r closingRepository) DeleteOverride(context.Context, domain.Request, string, time.Time) error {
	return r.err
}

type countingNudge struct{ n int }

func (c *countingNudge) Nudge() { c.n++ }

func TestEveryWriteThatCanMoveACloseNudgesTheSchedulerOnceItCommits(t *testing.T) {
	ctx := context.Background()
	writes := map[string]func(*application.Application) error{
		"create": func(a *application.Application) error {
			_, err := a.Commands.Create.Handle(ctx, command.Create{})
			return err
		},
		"update": func(a *application.Application) error {
			_, err := a.Commands.Update.Handle(ctx, command.Update{})
			return err
		},
		"reopen": func(a *application.Application) error {
			_, err := a.Commands.Reopen.Handle(ctx, command.Reopen{})
			return err
		},
		"extend": func(a *application.Application) error {
			_, err := a.Commands.Extend.Handle(ctx, command.Extend{Minutes: 30})
			return err
		},
		"set overrides": func(a *application.Application) error {
			_, err := a.Commands.SetOverrides.Handle(ctx, command.SetOverrides{Input: domain.OverrideInput{Reason: "Máy hỏng"}})
			return err
		},
		"delete override": func(a *application.Application) error {
			_, err := a.Commands.DeleteOverride.Handle(ctx, command.DeleteOverride{StudentID: "student"})
			return err
		},
	}
	refused := errors.New("refused")
	for name, write := range writes {
		nudge := &countingNudge{}
		if err := write(application.New(closingRepository{}).WithScheduleNudge(nudge)); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if nudge.n != 1 {
			t.Errorf("%s nudged %d times, want once", name, nudge.n)
		}
		failed := &countingNudge{}
		if err := write(application.New(closingRepository{err: refused}).WithScheduleNudge(failed)); !errors.Is(err, refused) {
			t.Fatalf("%s: %v, want the store's refusal", name, err)
		}
		if failed.n != 0 {
			t.Errorf("a refused %s nudged the scheduler", name)
		}
		if err := write(application.New(closingRepository{})); err != nil {
			t.Errorf("%s without a scheduler: %v", name, err)
		}
	}
}
