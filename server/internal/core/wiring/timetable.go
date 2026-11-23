package wiring

import (
	"quizzivy/internal/core/adapters"
	assignmentsapp "quizzivy/internal/modules/assignments/application"
	assignmentsrepo "quizzivy/internal/modules/assignments/repositories"
	identityapp "quizzivy/internal/modules/identity/application"
	organizationapp "quizzivy/internal/modules/organization/application"
	timetableapp "quizzivy/internal/modules/timetable/application"
	timetablehttp "quizzivy/internal/modules/timetable/http"
	timetablerepo "quizzivy/internal/modules/timetable/repositories"
	"quizzivy/internal/platform/db"
)

// timetable keeps the classes' weekly series and sessions (T-R8.4) and their
// attendance (T-R8.6), reading each caller's zone and signing a register's
// photos through identity, and running a new series to the end of its term
// and a rate over the current one through the organization's terms. Class
// scope is its repository's, checked under the class row's lock in the write
// it guards. The calendar lists the terms ahead and reads the assignment
// windows through the assignments module's CalendarWindows query (T-R8.5).
func timetable(dbx db.Context, identity *identityapp.Application, organization *organizationapp.Application) (*timetableapp.Application, timetablehttp.Timetable) {
	terms := adapters.Terms{Current: organization.Queries.CurrentTerm, EndingFrom: organization.Queries.TermsEndingFrom}
	assignments := adapters.TimetableAssignments{Query: assignmentsapp.New(assignmentsrepo.NewPostgres(dbx)).Queries.Calendar}
	app := timetableapp.New(timetablerepo.NewPostgres(dbx)).
		WithZones(adapters.ProfileZone{Query: identity.Queries.EffectiveZone}).
		WithTerms(terms).
		WithCalendar(terms, assignments).
		WithAvatars(adapters.TeacherPhotos{Query: identity.Queries.AvatarURL})
	return app, timetablehttp.NewTimetable(app)
}
