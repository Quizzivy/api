// Package application is the organization use cases: reading and saving the centre's settings, uploading its logos and sweeping the ones nothing names, judging a sign-in policy, counting what a retention policy would remove, answering the sign-in policy and branding other modules act on from a snapshot every server refreshes at most every 30 seconds, and keeping the centre's terms, which other modules read through CurrentTerm and TermsEndingFrom.
package application

import (
	"log/slog"
	"time"

	"quizzivy/internal/modules/organization/application/command"
	"quizzivy/internal/modules/organization/application/internal/support"
	"quizzivy/internal/modules/organization/application/model"
	"quizzivy/internal/modules/organization/application/ports"
	"quizzivy/internal/modules/organization/application/query"
	"quizzivy/internal/modules/organization/domain"
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
	UpdateSettings cqrs.CommandHandler[command.UpdateSettings, domain.Settings]
	UploadLogo     cqrs.CommandHandler[command.UploadLogo, command.UploadedLogo]
	SweepLogos     cqrs.CommandHandler[command.SweepLogos, command.SweptLogos]
	CreateTerm     cqrs.CommandHandler[command.CreateTerm, domain.StatedTerm]
	UpdateTerm     cqrs.CommandHandler[command.UpdateTerm, domain.StatedTerm]
	DeleteTerm     cqrs.CommandHandler[command.DeleteTerm, cqrs.Nothing]
}

// Queries are the module's reads.
type Queries struct {
	Settings        cqrs.QueryHandler[query.Settings, domain.Settings]
	Current         cqrs.QueryHandler[query.Current, domain.Settings]
	SignInPolicy    cqrs.QueryHandler[query.SignInPolicy, model.SignInPolicy]
	Branding        cqrs.QueryHandler[query.Branding, model.Branding]
	SignInImpact    cqrs.QueryHandler[query.SignInImpact, domain.Impact]
	RetentionImpact cqrs.QueryHandler[query.RetentionImpact, domain.RetentionImpact]
	Logo            cqrs.QueryHandler[query.Logo, query.LogoBytes]
	ListTerms       cqrs.QueryHandler[query.ListTerms, []domain.StatedTerm]
	// CurrentTerm and TermsEndingFrom are the terms as other modules read them
	// (model.Term): the timetable's series ends and attendance rates, the
	// calendar and R9's gradebook.
	CurrentTerm     cqrs.QueryHandler[query.CurrentTerm, *model.Term]
	TermsEndingFrom cqrs.QueryHandler[query.TermsEndingFrom, []model.Term]
}

// New builds the application over the repository, with one snapshot shared
// by every caller. googleConfigured says whether this server can sign anyone
// in with Google, which decides whether Google counts as a way in.
func New(repo domain.Repository, logger *slog.Logger, googleConfigured bool) *Application {
	service := &support.Service{
		Repo: repo, Snapshot: support.NewSnapshot(repo, logger), Now: time.Now, GoogleConfigured: googleConfigured,
	}
	return &Application{
		Commands: Commands{
			UpdateSettings: command.UpdateSettingsHandler{Service: service},
			UploadLogo:     command.UploadLogoHandler{Service: service},
			SweepLogos:     command.SweepLogosHandler{Service: service},
			CreateTerm:     command.CreateTermHandler{Service: service},
			UpdateTerm:     command.UpdateTermHandler{Service: service},
			DeleteTerm:     command.DeleteTermHandler{Service: service},
		},
		Queries: Queries{
			Settings:        query.SettingsHandler{Service: service},
			Current:         query.CurrentHandler{Service: service},
			SignInPolicy:    query.SignInPolicyHandler{Service: service},
			Branding:        query.BrandingHandler{Service: service},
			SignInImpact:    query.SignInImpactHandler{Service: service},
			RetentionImpact: query.RetentionImpactHandler{Service: service},
			Logo:            query.LogoHandler{Service: service},
			ListTerms:       query.ListTermsHandler{Service: service},
			CurrentTerm:     query.CurrentTermHandler{Service: service},
			TermsEndingFrom: query.TermsEndingFromHandler{Service: service},
		},
		service: service,
	}
}

// WithLogos returns the application storing logos in store, each checked by
// checker first. Without them an upload answers domain.ErrStorageOff.
func (a *Application) WithLogos(store ports.LogoStore, checker ports.LogoChecker) *Application {
	a.service.Logos, a.service.Checker = store, checker
	return a
}

// WithMail returns the application waking mail when a save changes the usage
// limits, so that mail a raised cap held goes out at once.
func (a *Application) WithMail(mail ports.MailWake) *Application {
	a.service.Mail = mail
	return a
}

// WithTerms returns the application keeping the centre's terms in terms.
// Without it every terms operation fails with support.ErrNoTerms.
func (a *Application) WithTerms(terms domain.Terms) *Application {
	a.service.Terms = terms
	return a
}

// WithZones returns the application counting an admin's today in the zone
// their profile names. Without it every account is read in
// domain.DefaultZone.
func (a *Application) WithZones(zones ports.Zones) *Application {
	a.service.Zones = zones
	return a
}

// SetClock replaces the time source of the snapshot and of the saves. Tests only.
func (a *Application) SetClock(now func() time.Time) {
	a.service.Now = now
	a.service.Snapshot.SetClock(now)
}
