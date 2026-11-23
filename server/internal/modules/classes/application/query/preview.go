package query

import (
	"context"
	"quizzivy/internal/modules/classes/application/internal/support"
	"quizzivy/internal/modules/classes/domain"
)

// Preview backs the /join/:code/confirm step (§6.2), which exists so a student
// sees WHICH class they are joining before authenticating. Only a valid,
// usable code reads the class's weekly pattern and room (R8, D5): every
// refusal answers before the series are read.
type Preview struct {
	Code string
}

type PreviewHandler struct {
	*support.Enrolment
}

func (s PreviewHandler) Handle(ctx context.Context, q Preview) (domain.PreviewResult, error) {
	on, err := s.JoinCodesOn(ctx)
	if err != nil {
		return domain.PreviewResult{}, err
	}
	if !on {
		return domain.PreviewResult{Outcome: domain.PreviewInvalid}, nil
	}
	code, ok := s.Lookup(q.Code)
	if !ok {
		return domain.PreviewResult{Outcome: domain.PreviewInvalid}, nil
	}

	row, err := s.Repo.LookupByCode(ctx, code)
	if err != nil {
		return domain.PreviewResult{}, err
	}
	if row == nil || !code.Matches(row.Lookup) {
		return domain.PreviewResult{Outcome: domain.PreviewInvalid}, nil
	}

	if outcome := row.Usable(s.Now()); outcome != domain.PreviewOK {
		return domain.PreviewResult{Outcome: outcome}, nil
	}

	if row.TeacherName == nil || *row.TeacherName == "" {
		return domain.PreviewResult{}, domain.ErrNoTeacher
	}

	timetable, err := s.Timetable(ctx, row.ClassID)
	if err != nil {
		return domain.PreviewResult{}, err
	}
	return domain.PreviewResult{
		Outcome:     domain.PreviewOK,
		ClassID:     row.ClassID,
		ClassName:   row.ClassName,
		TeacherName: *row.TeacherName,
		Schedule:    domain.ScheduleOf(row.ScheduleLabel, timetable),
		Room:        domain.RoomOf(row.Room, timetable),
	}, nil
}
