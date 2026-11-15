package wiring

import (
	"log/slog"

	leadsapp "quizzivy/internal/modules/leads/application"
	leadshttp "quizzivy/internal/modules/leads/http"
	leadsrepo "quizzivy/internal/modules/leads/repositories"
	"quizzivy/internal/platform/db"
	"quizzivy/internal/platform/email"
)

// leads stores the consultation requests and writes the notification of each
// stored one to outbox, in the transaction that stores it.
func leads(dbx db.Context, outbox *email.Outbox, logger *slog.Logger) leadshttp.Leads {
	return leadshttp.NewLeads(leadsapp.New(leadsrepo.NewPostgres(dbx, outbox), logger))
}
