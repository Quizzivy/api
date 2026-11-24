package wiring

import (
	"log/slog"

	"quizzivy/internal/core/adapters"
	attemptsapp "quizzivy/internal/modules/attempts/application"
	identityapp "quizzivy/internal/modules/identity/application"
	messagingapp "quizzivy/internal/modules/messaging/application"
	messaginghttp "quizzivy/internal/modules/messaging/http"
	messagingrepo "quizzivy/internal/modules/messaging/repositories"
	notificationsapp "quizzivy/internal/modules/notifications/application"
	"quizzivy/internal/platform/db"
	"quizzivy/internal/platform/storage"
)

// messaging keeps class channels, direct threads and colleague threads (D17,
// F2), signing the photos they show through identity and reading the flagged
// attempts a staff member's direct thread shows through attempts. Its posts,
// reads, pointers and uploads write no audit row: the append-only messages
// table is the record. It serves the staff side and the student side. Messages' files are
// kept in the media bucket, objects, under messages/ (T-R7.11); without a
// bucket (a nil objects) an upload and a download answer 501 and the
// housekeeping has no file step. Each new message leaves its readers an
// in-app item through notifications, which counts a student's unread messages
// for the shell's summary through messaging (T-R7.12).
func messaging(logger *slog.Logger, dbx db.Context, objects *storage.Client, identity *identityapp.Application, attempts *attemptsapp.Application, notifications *notificationsapp.Application) (*messagingapp.Application, messaginghttp.Staff, messaginghttp.Student) {
	app := messagingapp.New(messagingrepo.NewPostgres(dbx)).
		WithAvatars(adapters.MessagingPhotos{Query: identity.Queries.AvatarURL}).
		WithContextEvents(adapters.MessagingContextEvents{Query: attempts.Queries.FlaggedAttempts}).
		WithBell(adapters.MessagingBell{Notify: notifications.Commands.Notify, MarkKeyRead: notifications.Commands.MarkKeyRead}, logger)
	notifications.WithUnreadMessages(app.Queries.StudentUnread)
	if objects != nil {
		app.WithAttachments(adapters.MessagingAttachments{Objects: objects}, adapters.MessagingAudio{})
		logger.Info("message attachments enabled")
	} else {
		logger.Info("message attachments disabled (no bucket configured)")
	}
	return app, messaginghttp.NewStaff(app), messaginghttp.NewStudent(app)
}
