package wiring

import (
	"quizzivy/internal/core/adapters"
	attemptsapp "quizzivy/internal/modules/attempts/application"
	identityapp "quizzivy/internal/modules/identity/application"
	messagingapp "quizzivy/internal/modules/messaging/application"
	messaginghttp "quizzivy/internal/modules/messaging/http"
	messagingrepo "quizzivy/internal/modules/messaging/repositories"
	"quizzivy/internal/platform/db"
)

// messaging keeps class channels and direct threads (D17), signing the photos
// they show through identity and reading the flagged attempts a staff
// member's direct thread shows through attempts. Its posts, reads and
// pointers write no audit row: the append-only messages table is the record.
// It serves the staff side and the student side.
func messaging(dbx db.Context, identity *identityapp.Application, attempts *attemptsapp.Application) (*messagingapp.Application, messaginghttp.Staff, messaginghttp.Student) {
	app := messagingapp.New(messagingrepo.NewPostgres(dbx)).
		WithAvatars(adapters.MessagingPhotos{Query: identity.Queries.AvatarURL}).
		WithContextEvents(adapters.MessagingContextEvents{Query: attempts.Queries.FlaggedAttempts})
	return app, messaginghttp.NewStaff(app), messaginghttp.NewStudent(app)
}
