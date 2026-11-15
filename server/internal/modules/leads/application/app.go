// Package application is the consultation requests' use cases: taking a request from the landing site's form, and an Admin's list, read, change and removal of the stored ones.
package application

import (
	"log/slog"
	"time"

	"quizzivy/internal/modules/leads/application/command"
	"quizzivy/internal/modules/leads/application/internal/support"
	"quizzivy/internal/modules/leads/application/query"
	"quizzivy/internal/modules/leads/domain"
	"quizzivy/internal/shared/cqrs"
)

// Application is every use case of the module.
type Application struct {
	Commands Commands
	Queries  Queries
	service  *support.Service
}

// Commands are the module's writes.
type Commands struct {
	Submit cqrs.CommandHandler[command.Submit, cqrs.Nothing]
	Update cqrs.CommandHandler[command.Update, domain.Lead]
	Delete cqrs.CommandHandler[command.Delete, cqrs.Nothing]
}

// Queries are the module's reads.
type Queries struct {
	List cqrs.QueryHandler[query.List, query.Listed]
	Get  cqrs.QueryHandler[query.Get, domain.Lead]
}

// New builds the application over repo. A request that is answered and not
// stored is noted in logger, by its reason alone.
func New(repo domain.Repository, logger *slog.Logger) *Application {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	service := &support.Service{Repo: repo, Now: time.Now, Logger: logger}
	return &Application{
		Commands: Commands{
			Submit: command.SubmitHandler{Service: service},
			Update: command.UpdateHandler{Service: service},
			Delete: command.DeleteHandler{Service: service},
		},
		Queries: Queries{
			List: query.ListHandler{Service: service},
			Get:  query.GetHandler{Service: service},
		},
		service: service,
	}
}

// SetClock replaces the time source of the writes. Tests only.
func (a *Application) SetClock(now func() time.Time) { a.service.Now = now }
