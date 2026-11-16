// Package application is the sharing context's use cases: the directory of
// colleagues, granting, changing and stopping a share, and Shared with me.
package application

import (
	"log/slog"
	"time"

	"quizzivy/internal/modules/sharing/application/command"
	"quizzivy/internal/modules/sharing/application/internal/support"
	"quizzivy/internal/modules/sharing/application/ports"
	"quizzivy/internal/modules/sharing/application/query"
	"quizzivy/internal/modules/sharing/domain"
	"quizzivy/internal/shared/cqrs"
)

// Application is every use case of the module: commands change it, queries read it.
type Application struct {
	Commands Commands
	Queries  Queries
	service  *support.Service
}

// Commands are the module's writes.
type Commands struct {
	CreateShares       cqrs.CommandHandler[command.CreateShares, domain.Granted]
	UpdateShare        cqrs.CommandHandler[command.UpdateShare, domain.Grant]
	DeleteShare        cqrs.CommandHandler[command.DeleteShare, cqrs.Nothing]
	RemoveSharedWithMe cqrs.CommandHandler[command.RemoveSharedWithMe, cqrs.Nothing]
}

// Queries are the module's reads.
type Queries struct {
	Directory         cqrs.QueryHandler[query.Directory, []domain.Person]
	ListShares        cqrs.QueryHandler[query.ListShares, query.Listing]
	SharedWithMe      cqrs.QueryHandler[query.SharedWithMe, query.SharedPage]
	CountSharedWithMe cqrs.QueryHandler[query.CountSharedWithMe, int]
}

// New builds the application over repo, resolving the tests and question sets
// grants name through content.
func New(repo domain.Repository, content ports.Content) *Application {
	service := &support.Service{Repo: repo, Content: content, Logger: slog.New(slog.DiscardHandler), Now: time.Now}
	return &Application{
		Commands: Commands{
			CreateShares:       command.CreateSharesHandler{Service: service},
			UpdateShare:        command.UpdateShareHandler{Service: service},
			DeleteShare:        command.DeleteShareHandler{Service: service},
			RemoveSharedWithMe: command.RemoveSharedWithMeHandler{Service: service},
		},
		Queries: Queries{
			Directory:         query.DirectoryHandler{Service: service},
			ListShares:        query.ListSharesHandler{Service: service},
			SharedWithMe:      query.SharedWithMeHandler{Service: service},
			CountSharedWithMe: query.CountSharedWithMeHandler{Service: service},
		},
		service: service,
	}
}

// WithAvatars signs the profile photos the directory and the grants show.
// Without it they show none.
func (a *Application) WithAvatars(avatars ports.Avatars) *Application {
	a.service.Avatars = avatars
	return a
}

// WithNotifier tells new recipients through notifier, logging to logger a
// notification it could not deliver. Without it nobody is told.
func (a *Application) WithNotifier(notifier ports.Notifier, logger *slog.Logger) *Application {
	a.service.Notifier = notifier
	if logger != nil {
		a.service.Logger = logger
	}
	return a
}

// SetClock replaces the time source of the writes. Tests only.
func (a *Application) SetClock(now func() time.Time) { a.service.Now = now }
