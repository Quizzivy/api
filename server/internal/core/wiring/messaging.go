package wiring

import (
	"quizzivy/internal/core/adapters"
	identityapp "quizzivy/internal/modules/identity/application"
	messagingapp "quizzivy/internal/modules/messaging/application"
	messaginghttp "quizzivy/internal/modules/messaging/http"
	messagingrepo "quizzivy/internal/modules/messaging/repositories"
	"quizzivy/internal/platform/db"
)

// messaging keeps class channels and direct threads (D17), signing the photos
// they show through identity. Its posts, reads and pointers write no audit
// row: the append-only messages table is the record.
func messaging(dbx db.Context, identity *identityapp.Application) (*messagingapp.Application, messaginghttp.Staff) {
	app := messagingapp.New(messagingrepo.NewPostgres(dbx)).
		WithAvatars(adapters.MessagingPhotos{Query: identity.Queries.AvatarURL})
	return app, messaginghttp.NewStaff(app)
}
