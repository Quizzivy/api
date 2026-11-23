// Package ports is what the timetable needs from outside it: the term that
// holds a date, from the organization module, and the calendar zone of an
// account's profile, from identity.
package ports

import (
	"context"
	"time"

	organizationmodel "quizzivy/internal/modules/organization/application/model"
)

// Terms answers the term that holds the calendar day of on, read in on's own
// location, or nil when no term holds it (adapters.Terms).
type Terms interface {
	CurrentTerm(ctx context.Context, on time.Time) (*organizationmodel.Term, error)
}

// Zones resolves the calendar zone an account's profile names, the centre's
// when it names none (adapters.ProfileZone).
type Zones interface {
	ZoneOf(ctx context.Context, userID string) (string, error)
}
