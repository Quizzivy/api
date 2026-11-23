// Package application is the timetable's use cases (T-R8.4): adding a session
// to a class once or every week, changing or cancelling one date, removing a
// one-off, changing a weekly session from a date on and stopping it, and the
// reads that compute the sessions of a range and the calendar (T-R8.5); and
// its attendance (T-R8.6): a session's register, saving it, and the sessions
// that take one; and a student's week (T-R8.8).
package application

import (
	"time"

	"quizzivy/internal/modules/timetable/application/command"
	"quizzivy/internal/modules/timetable/application/internal/support"
	"quizzivy/internal/modules/timetable/application/ports"
	"quizzivy/internal/modules/timetable/application/query"
	"quizzivy/internal/modules/timetable/domain"
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
	CreateSession cqrs.CommandHandler[command.CreateSession, domain.Session]
	UpdateSession cqrs.CommandHandler[command.UpdateSession, domain.Session]
	DeleteSession cqrs.CommandHandler[command.DeleteSession, cqrs.Nothing]
	UpdateSeries  cqrs.CommandHandler[command.UpdateSeries, domain.SeriesEdit]
	EndSeries     cqrs.CommandHandler[command.EndSeries, *domain.Series]

	SaveAttendance cqrs.CommandHandler[command.SaveAttendance, domain.Register]
}

// Queries are the module's reads.
type Queries struct {
	Occurrences cqrs.QueryHandler[query.Occurrences, []domain.Session]
	GetSession  cqrs.QueryHandler[query.GetSession, domain.Session]
	Calendar    cqrs.QueryHandler[query.Calendar, query.CalendarView]

	AttendanceSessions cqrs.QueryHandler[query.AttendanceSessions, domain.SessionPage]
	SessionAttendance  cqrs.QueryHandler[query.SessionAttendance, domain.Register]

	StudentWeek cqrs.QueryHandler[query.StudentWeek, domain.Week]

	// ActiveSeries is the classes module's ScheduleSource (adapters.ClassSchedules).
	ActiveSeries cqrs.QueryHandler[query.ActiveSeries, []domain.Series]
}

// New builds the application over repo. Until WithTerms no term holds any
// date, and until WithZones every account is read in domain.DefaultZone.
func New(repo domain.Repository) *Application {
	service := &support.Service{Repo: repo, Now: time.Now}
	return &Application{
		Commands: Commands{
			CreateSession: command.CreateSessionHandler{Service: service},
			UpdateSession: command.UpdateSessionHandler{Service: service},
			DeleteSession: command.DeleteSessionHandler{Service: service},
			UpdateSeries:  command.UpdateSeriesHandler{Service: service},
			EndSeries:     command.EndSeriesHandler{Service: service},

			SaveAttendance: command.SaveAttendanceHandler{Service: service},
		},
		Queries: Queries{
			Occurrences: query.OccurrencesHandler{Service: service},
			GetSession:  query.GetSessionHandler{Service: service},
			Calendar:    query.CalendarHandler{Service: service},

			AttendanceSessions: query.AttendanceSessionsHandler{Service: service},
			SessionAttendance:  query.SessionAttendanceHandler{Service: service},

			StudentWeek: query.StudentWeekHandler{Service: service},

			ActiveSeries: query.ActiveSeriesHandler{Service: service},
		},
		service: service,
	}
}

// WithTerms runs a new weekly series to the end of the term that holds its
// first date, through terms.
func (a *Application) WithTerms(terms ports.Terms) *Application {
	a.service.Terms = terms
	return a
}

// WithCalendar lists the terms ahead on the calendar through terms and draws
// the assignment windows assignments reads beside its sessions. Until then the
// calendar holds sessions only.
func (a *Application) WithCalendar(terms ports.TermsAhead, assignments ports.Assignments) *Application {
	a.Queries.Calendar = query.CalendarHandler{Service: a.service, Terms: terms, Assignments: assignments}
	return a
}

// WithDueItems lists in a student's week the assignments due reads. Until
// then the week holds sessions only.
func (a *Application) WithDueItems(due ports.DueItems) *Application {
	a.Queries.StudentWeek = query.StudentWeekHandler{Service: a.service, Due: due}
	return a
}

// WithZones reads each caller's dates and times in the zone their profile
// names, through zones.
func (a *Application) WithZones(zones ports.Zones) *Application {
	a.service.Zones = zones
	return a
}

// WithAvatars signs the profile photos a register shows, through avatars.
func (a *Application) WithAvatars(avatars ports.Avatars) *Application {
	a.service.Avatars = avatars
	return a
}

// SetClock replaces the time source of the writes. Tests only.
func (a *Application) SetClock(now func() time.Time) { a.service.Now = now }
