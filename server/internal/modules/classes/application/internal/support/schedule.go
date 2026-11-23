package support

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"quizzivy/internal/modules/classes/application/ports"
	"quizzivy/internal/modules/classes/domain"
)

// timetables derives the Timetable of each class in ids from one read of
// their active series. Without a source, or with no ids, no class has a
// series, and none is read.
func timetables(ctx context.Context, source ports.ScheduleSource, now time.Time, ids []string) (map[string]domain.Timetable, error) {
	if source == nil || len(ids) == 0 {
		return nil, nil
	}
	series, err := source.ActiveSeries(ctx, ids, now)
	if err != nil {
		return nil, fmt.Errorf("classes: the weekly series of %d classes: %w", len(ids), err)
	}
	out := make(map[string]domain.Timetable, len(series))
	for classID, active := range series {
		out[classID] = domain.TimetableOf(active)
	}
	return out, nil
}

// AttachSchedule sets Timetable on each class from one read of the classes'
// active series. The classes were reached by the caller already, so no scope
// is applied here.
func (s *Service) AttachSchedule(ctx context.Context, classes ...*domain.Class) error {
	ids := make([]string, len(classes))
	for i, c := range classes {
		ids[i] = c.ID
	}
	found, err := timetables(ctx, s.Schedules, s.Now(), ids)
	if err != nil {
		return err
	}
	for _, c := range classes {
		c.Timetable = found[c.ID]
	}
	return nil
}

// AttachMySchedule sets Timetable on each of a student's classes from one
// read of their active series.
func (s *Service) AttachMySchedule(ctx context.Context, classes []domain.MyClass) error {
	ids := make([]string, len(classes))
	for i, c := range classes {
		ids[i] = c.ID
	}
	found, err := timetables(ctx, s.Schedules, s.Now(), ids)
	if err != nil {
		return err
	}
	for i := range classes {
		classes[i].Timetable = found[classes[i].ID]
	}
	return nil
}

// Timetable is the Timetable of class classID, which a valid join code
// reached.
func (s *Enrolment) Timetable(ctx context.Context, classID string) (domain.Timetable, error) {
	found, err := timetables(ctx, s.Schedules, s.Now(), []string{classID})
	if err != nil {
		return domain.Timetable{}, err
	}
	return found[classID], nil
}

// Schedule sets the timetable of the class a committed enrolment placed the
// student in. The enrolment stands whatever happens here: a failure to read
// the series is logged, and the class answers with its label and its own
// room alone.
func (s *Enrolment) Schedule(ctx context.Context, result *domain.EnrolResult) {
	if result.Outcome != domain.PreviewOK {
		return
	}
	timetable, err := s.Timetable(ctx, result.Class.ID)
	if err != nil {
		if s.Logger != nil {
			s.Logger.WarnContext(ctx, "the schedule of a joined class could not be read", slog.String("class_id", result.Class.ID), slog.Any("err", err))
		}
		return
	}
	result.Class.Timetable = &timetable
}
