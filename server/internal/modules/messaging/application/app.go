// Package application is the messaging context's use cases on the staff side
// (T-R7.10a): the Messages list, a direct thread with a student, a class's
// announcement channel, posting in each, and the read pointer.
package application

import (
	"quizzivy/internal/modules/messaging/application/command"
	"quizzivy/internal/modules/messaging/application/internal/support"
	"quizzivy/internal/modules/messaging/application/ports"
	"quizzivy/internal/modules/messaging/application/query"
	"quizzivy/internal/modules/messaging/domain"
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
	PostDirect       cqrs.CommandHandler[command.PostDirect, domain.Posted]
	PostAnnouncement cqrs.CommandHandler[command.PostAnnouncement, domain.Posted]
	MarkRead         cqrs.CommandHandler[command.MarkRead, cqrs.Nothing]
}

// Queries are the module's reads.
type Queries struct {
	StaffThreads      cqrs.QueryHandler[query.StaffThreads, []domain.StaffThread]
	StaffDirectThread cqrs.QueryHandler[query.StaffDirectThread, query.DirectThread]
	StaffClassThread  cqrs.QueryHandler[query.StaffClassThread, query.ClassThread]
}

// New builds the application over repo.
func New(repo domain.Repository) *Application {
	service := &support.Service{Repo: repo}
	return &Application{
		Commands: Commands{
			PostDirect:       command.PostDirectHandler{Service: service},
			PostAnnouncement: command.PostAnnouncementHandler{Service: service},
			MarkRead:         command.MarkReadHandler{Service: service},
		},
		Queries: Queries{
			StaffThreads:      query.StaffThreadsHandler{Service: service},
			StaffDirectThread: query.StaffDirectThreadHandler{Service: service},
			StaffClassThread:  query.StaffClassThreadHandler{Service: service},
		},
		service: service,
	}
}

// WithAvatars signs the profile photos the threads show. Without it they show
// none.
func (a *Application) WithAvatars(avatars ports.Avatars) *Application {
	a.service.Avatars = avatars
	return a
}
