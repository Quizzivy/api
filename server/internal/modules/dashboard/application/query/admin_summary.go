package query

import (
	"context"

	"quizzivy/internal/modules/dashboard/application/internal/support"
	"quizzivy/internal/modules/dashboard/domain"
)

// AdminSummary reads the Admin sidebar's counts for Viewer, each only for a viewer whose keys open it: the accounts on a temporary password, counted as the Overview's attention row counts them, with people.users.manage, and the consultation requests nobody has handled yet with system.leads.
type AdminSummary struct {
	Viewer domain.AdminViewer
}

type AdminSummaryHandler struct{ *support.Service }

func (s AdminSummaryHandler) Handle(ctx context.Context, q AdminSummary) (domain.AdminSummary, error) {
	var out domain.AdminSummary
	if q.Viewer.Users {
		n, err := s.Repo.MustChangePassword(ctx)
		if err != nil {
			return domain.AdminSummary{}, err
		}
		out.MustChangePassword = &n
	}
	if q.Viewer.Leads {
		n, err := s.Repo.NewLeads(ctx)
		if err != nil {
			return domain.AdminSummary{}, err
		}
		out.NewLeads = &n
	}
	return out, nil
}
