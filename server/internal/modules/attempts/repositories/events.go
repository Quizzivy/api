package repositories

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"quizzivy/internal/modules/attempts/domain"
	"time"

	"github.com/jackc/pgx/v5"
)

// Flush appends a batch of client events under the session the caller names.
// That session must be the attempt's current one or one the attempt has
// already recorded events under, so a tab that was taken over can still hand
// in what it holds, and a session id that was never part of the attempt
// writes nothing. It never changes which session is current. The time on the
// paper it reports is credited as a save's is (creditActive), from the
// current session alone. The milestones say that the integrity policy flagged
// the paper because of them, or counted another absence on a flexible one.
func (s *Postgres) Flush(ctx context.Context, in domain.FlushInput, now time.Time) (domain.Milestones, error) {
	var (
		studentID  string
		beaconHash []byte
		deadlineAt time.Time
		versionID  string
		known      bool
	)
	err := s.QueryRow(ctx, `
		SELECT a.student_id, a.beacon_token_hash, a.deadline_at, a.test_version_id,
		       a.session_id = $2::uuid
		       OR EXISTS (SELECT 1 FROM app.attempt_events e
		                   WHERE e.attempt_id = a.id AND e.session_id = $2::uuid)
		  FROM app.attempts a WHERE a.id = $1::uuid`, in.AttemptID, in.SessionID).
		Scan(&studentID, &beaconHash, &deadlineAt, &versionID, &known)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Milestones{}, domain.ErrForbidden
	}
	if err != nil {
		return domain.Milestones{}, fmt.Errorf("attempts: read attempt for flush: %w", err)
	}

	if err := authorizeFlush(in, studentID, beaconHash, deadlineAt, now); err != nil {
		return domain.Milestones{}, err
	}
	if !known {
		return domain.Milestones{}, domain.ErrForbidden
	}
	found, err := insertEvents(ctx, s.Conn(), in.AttemptID, in.SessionID, in.Events, versionID)
	if err != nil {
		return domain.Milestones{}, err
	}
	if err := creditActive(ctx, s.Conn(), in.AttemptID, in.SessionID, in.ActiveMs, now); err != nil {
		return domain.Milestones{}, err
	}
	return domain.Milestones{AttemptID: in.AttemptID, Flagged: found.flagged, AbsentAgain: found.again}, nil
}

func authorizeFlush(in domain.FlushInput, studentID string, beaconHash []byte, deadlineAt, now time.Time) error {
	switch {
	case in.StudentID != "":
		if in.StudentID != studentID {
			return domain.ErrForbidden
		}
		return nil

	case in.BeaconToken != "":
		presented := sha256.Sum256([]byte(in.BeaconToken))

		if subtle.ConstantTimeCompare(presented[:], beaconHash) != 1 {
			return domain.ErrForbidden
		}
		if now.After(deadlineAt) {
			return domain.ErrBeaconExpired
		}
		return nil
	}
	return domain.ErrForbidden
}
