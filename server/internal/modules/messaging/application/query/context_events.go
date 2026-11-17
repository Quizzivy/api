package query

import (
	"context"

	"quizzivy/internal/modules/messaging/application/internal/support"
	"quizzivy/internal/modules/messaging/domain"
	"quizzivy/internal/shared/access"
)

// contextEvents are the cards a staff member's direct thread with studentID
// shows beside its messages: the student's flagged attempts on assignments
// scope reaches, from ContextWindow before the thread's first message, oldest
// first. None while the thread has no message, or with no attempts reader
// wired. They are not messages, so no unread count, pointer or page sees
// them.
func contextEvents(ctx context.Context, s *support.Service, scope access.Scope, staffID, studentID string) ([]domain.ContextEvent, error) {
	if s.Events == nil {
		return []domain.ContextEvent{}, nil
	}
	first, err := s.Repo.FirstMessageAt(ctx, staffID, studentID)
	if err != nil || first == nil {
		return []domain.ContextEvent{}, err
	}
	events, err := s.Events.FlaggedAttempts(ctx, scope, studentID, first.Add(-domain.ContextWindow))
	if err != nil {
		return nil, err
	}
	if events == nil {
		events = []domain.ContextEvent{}
	}
	return events, nil
}
