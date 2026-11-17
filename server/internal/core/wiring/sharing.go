package wiring

import (
	"log/slog"

	"quizzivy/internal/core/adapters"
	identityapp "quizzivy/internal/modules/identity/application"
	questionsapp "quizzivy/internal/modules/questions/application"
	sharingapp "quizzivy/internal/modules/sharing/application"
	sharingports "quizzivy/internal/modules/sharing/application/ports"
	sharinghttp "quizzivy/internal/modules/sharing/http"
	sharingrepo "quizzivy/internal/modules/sharing/repositories"
	testsapp "quizzivy/internal/modules/tests/application"
	"quizzivy/internal/platform/db"
)

type sharingModules struct {
	tests     *testsapp.Application
	questions *questionsapp.Application
	identity  *identityapp.Application
	notifier  sharingports.Notifier
}

// sharing keeps the grants of tests and question sets, resolving each through
// its own module's read rule, signs the photos the share dialog shows through
// identity, and tells new recipients through notifications.
func sharing(dbx db.Context, m sharingModules, logger *slog.Logger) (*sharingapp.Application, sharinghttp.Sharing) {
	app := sharingapp.New(sharingrepo.NewPostgres(dbx), adapters.SharedContent{Tests: m.tests.Queries.Ownership, Sets: m.questions.Queries.GetSet}).
		WithAvatars(adapters.TeacherPhotos{Query: m.identity.Queries.AvatarURL}).
		WithNotifier(m.notifier, logger)
	return app, sharinghttp.NewSharing(app)
}
