package repositories

import (
	"context"
	"errors"
	"fmt"
	"quizzivy/internal/modules/attempts/domain"
	"quizzivy/internal/platform/db"
	"quizzivy/internal/shared/access"
	"quizzivy/internal/shared/opt"
	"quizzivy/internal/shared/visibility"
	"time"

	"github.com/jackc/pgx/v5"
)

type Timelines struct {
	db.Repository
	now func() time.Time
}

func NewTimelines(dbx db.Context) *Timelines {
	return &Timelines{Repository: db.NewRepository(dbx), now: time.Now}
}

// Timeline reads the log of one attempt on an assignment the scope reaches and
// builds §10.4's view of it; another teacher's answers ErrTimelineNotFound.
func (s *Timelines) Timeline(ctx context.Context, scope access.Scope, attemptID string) (domain.Timeline, error) {
	var (
		startedAt    time.Time
		minAwayMs    int
		audioReplays int
	)
	err := s.QueryRow(ctx, `
		SELECT at.started_at, a.integrity_min_away_ms,
		       coalesce((SELECT sum(greatest(p.plays - q.audio_max_plays, 0))
		                   FROM app.attempt_audio_plays p
		                   JOIN app.test_version_questions q ON q.id = p.question_id
		                  WHERE p.attempt_id = at.id AND q.audio_max_plays IS NOT NULL), 0)
		       + coalesce((SELECT sum(greatest(p.plays-r.max_plays,0))
		           FROM app.attempt_group_audio_plays p
		           JOIN app.test_version_group_recordings r ON r.id=p.recording_id
		           WHERE p.attempt_id=at.id AND r.max_plays IS NOT NULL),0)
		  FROM app.attempts at
		  JOIN app.assignments a ON a.id = at.assignment_id
		 WHERE at.id = $1::uuid AND ($2::boolean OR at.assignment_id IN `+visibility.AssignmentIDs(3)+`)`,
		attemptID, scope.All, opt.String(scope.UserID)).Scan(&startedAt, &minAwayMs, &audioReplays)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Timeline{}, domain.ErrTimelineNotFound
	}
	if err != nil {
		return domain.Timeline{}, fmt.Errorf("integrity: read attempt: %w", err)
	}

	rows, err := s.Query(ctx, `
		SELECT id, kind, occurred_at, received_at, client_seq, session_id::text,
		       question_id::text, meta
		  FROM app.attempt_events
		 WHERE attempt_id = $1::uuid
		 ORDER BY occurred_at, id`, attemptID)
	if err != nil {
		return domain.Timeline{}, fmt.Errorf("integrity: read events: %w", err)
	}
	defer rows.Close()

	var events []domain.IntegrityEvent
	for rows.Next() {
		var e domain.IntegrityEvent
		if err := rows.Scan(&e.ID, &e.Kind, &e.OccurredAt, &e.ReceivedAt, &e.ClientSeq,
			&e.SessionID, &e.QuestionID, &e.Meta); err != nil {
			return domain.Timeline{}, fmt.Errorf("integrity: scan event: %w", err)
		}
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		return domain.Timeline{}, fmt.Errorf("integrity: read events: %w", err)
	}
	return domain.Timelines.Build(startedAt, minAwayMs, audioReplays, events, s.now()), nil
}

// Flagged returns studentID's flagged attempts on the assignments scope
// reaches, as AssignmentIDs says, that visibility.Papers shows (the rule of
// the papers list, the grading queue and the dashboard, so a former student's
// paper on a colleague's assignment stays hidden), whose moment,
// coalesce(submitted_at, started_at), is since or later, oldest first. The
// title is the assignment's test's.
func (s *Timelines) Flagged(ctx context.Context, scope access.Scope, studentID string, since time.Time) ([]domain.FlaggedAttempt, error) {
	out, err := db.QueryMany(ctx, s, `
		SELECT at.id::text, at.assignment_id::text, t.title, at.focus_loss_count, coalesce(at.submitted_at, at.started_at) AS at
		  FROM app.attempts at
		  JOIN app.assignments a ON a.id = at.assignment_id
		  JOIN app.tests t ON t.id = a.test_id
		 WHERE at.student_id = $1::uuid AND at.flagged
		   AND coalesce(at.submitted_at, at.started_at) >= $2
		   AND ($3::boolean OR at.assignment_id IN `+visibility.AssignmentIDs(4)+`)
		   AND `+visibility.Papers(3, 4, "at.assignment_id", "at.student_id")+`
		 ORDER BY 5, at.id`, []any{studentID, since, scope.All, opt.String(scope.UserID)},
		func(rows pgx.Rows) (domain.FlaggedAttempt, error) {
			var f domain.FlaggedAttempt
			err := rows.Scan(&f.AttemptID, &f.AssignmentID, &f.Title, &f.FocusLost, &f.At)
			return f, err
		})
	if err != nil {
		return nil, fmt.Errorf("integrity: flagged attempts: %w", err)
	}
	return out, nil
}
