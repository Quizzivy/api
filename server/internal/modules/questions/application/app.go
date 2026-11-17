package application

import (
	"quizzivy/internal/modules/questions/application/command"
	"quizzivy/internal/modules/questions/application/internal/support"
	"quizzivy/internal/modules/questions/application/ports"
	"quizzivy/internal/modules/questions/application/query"
	"quizzivy/internal/modules/questions/domain"
	"quizzivy/internal/shared/cqrs"
)

// Application is every use case of the module: commands change it, queries read it.
type Application struct {
	Commands Commands
	Queries  Queries
	service  *support.Service
}

// Commands are the module's writes. The question-set commands are nil until
// the application is given its sets (WithSets).
type Commands struct {
	AddTags   cqrs.CommandHandler[command.AddTags, int]
	Create    cqrs.CommandHandler[command.Create, domain.Question]
	Delete    cqrs.CommandHandler[command.Delete, cqrs.Nothing]
	Duplicate cqrs.CommandHandler[command.Duplicate, domain.Question]
	Update    cqrs.CommandHandler[command.Update, domain.Question]

	AddToSet      cqrs.CommandHandler[command.AddToSet, domain.QuestionSet]
	CopySet       cqrs.CommandHandler[command.CopySet, domain.SetCopy]
	CreateSet     cqrs.CommandHandler[command.CreateSet, domain.QuestionSet]
	DeleteSet     cqrs.CommandHandler[command.DeleteSet, cqrs.Nothing]
	RemoveFromSet cqrs.CommandHandler[command.RemoveFromSet, domain.QuestionSet]
	UpdateSet     cqrs.CommandHandler[command.UpdateSet, domain.QuestionSet]
}

// Queries are the module's reads. The question-set queries are nil until the
// application is given its sets (WithSets).
type Queries struct {
	Counts              cqrs.QueryHandler[query.Counts, query.CountsResult]
	Facets              cqrs.QueryHandler[query.Facets, domain.TypeFacets]
	Get                 cqrs.QueryHandler[query.Get, domain.Question]
	GetIncludingDeleted cqrs.QueryHandler[query.GetIncludingDeleted, domain.Question]
	List                cqrs.QueryHandler[query.List, query.ListResult]
	Tags                cqrs.QueryHandler[query.Tags, []string]

	GetSet   cqrs.QueryHandler[query.GetSet, domain.QuestionSet]
	ListSets cqrs.QueryHandler[query.ListSets, []domain.QuestionSet]
}

func New(repo domain.Repository, media ports.MediaKinds) *Application {
	service := support.NewService(repo, media)
	return &Application{
		Commands: Commands{
			AddTags:   command.AddTagsHandler{Service: service},
			Create:    command.CreateHandler{Service: service},
			Delete:    command.DeleteHandler{Service: service},
			Duplicate: command.DuplicateHandler{Service: service},
			Update:    command.UpdateHandler{Service: service},
		},
		Queries: Queries{
			Counts:              query.CountsHandler{Service: service},
			Facets:              query.FacetsHandler{Service: service},
			Get:                 query.GetHandler{Service: service},
			GetIncludingDeleted: query.GetIncludingDeletedHandler{Service: service},
			List:                query.ListHandler{Service: service},
			Tags:                query.TagsHandler{Service: service},
		},
		service: service,
	}
}

// WithTests gives the application the tests module's edit rule, which a bank
// write made for a test (forTestId) is decided by, and returns it.
func (a *Application) WithTests(tests ports.EditableTests) *Application {
	a.service.Tests = tests
	return a
}

// WithSets gives the application its question sets and the write contexts a
// bank write may name (forTestId, setId), and returns it.
func (a *Application) WithSets(sets domain.SetRepository, contexts domain.WriteContexts) *Application {
	a.service.Sets, a.service.Contexts = sets, contexts
	a.Commands.AddToSet = command.AddToSetHandler{Service: a.service}
	a.Commands.CopySet = command.CopySetHandler{Service: a.service}
	a.Commands.CreateSet = command.CreateSetHandler{Service: a.service}
	a.Commands.DeleteSet = command.DeleteSetHandler{Service: a.service}
	a.Commands.RemoveFromSet = command.RemoveFromSetHandler{Service: a.service}
	a.Commands.UpdateSet = command.UpdateSetHandler{Service: a.service}
	a.Queries.GetSet = query.GetSetHandler{Service: a.service}
	a.Queries.ListSets = query.ListSetsHandler{Service: a.service}
	return a
}
