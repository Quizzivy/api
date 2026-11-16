// Package application is the messaging context's use cases. On the staff side
// (T-R7.10a): the Messages list, a direct thread with a student, a class's
// announcement channel, posting in each, and the read pointer. On the student
// side (T-R7.10b): the student's Messages, a direct thread with a teacher,
// which they post in as text, a class's channel, which they read only, and the
// read pointer. Staff attach files to their posts (T-R7.11): an upload that
// waits unsent, a download for the thread's participants, and the daily
// sweeps of files nobody sent and of objects no row names.
package application

import (
	"time"

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

	PostAsStudent     cqrs.CommandHandler[command.PostAsStudent, domain.StudentPosted]
	MarkReadAsStudent cqrs.CommandHandler[command.MarkReadAsStudent, cqrs.Nothing]

	UploadAttachment cqrs.CommandHandler[command.UploadAttachment, domain.Attachment]
	// The housekeeping steps, nil until WithAttachments.
	SweepUnsentAttachments cqrs.CommandHandler[command.SweepUnsentAttachments, int]
	SweepAttachmentObjects cqrs.CommandHandler[command.SweepAttachmentObjects, int]
}

// Queries are the module's reads.
type Queries struct {
	StaffThreads      cqrs.QueryHandler[query.StaffThreads, []domain.StaffThread]
	StaffDirectThread cqrs.QueryHandler[query.StaffDirectThread, query.DirectThread]
	StaffClassThread  cqrs.QueryHandler[query.StaffClassThread, query.ClassThread]

	StudentThreads      cqrs.QueryHandler[query.StudentThreads, []domain.StudentThread]
	StudentDirectThread cqrs.QueryHandler[query.StudentDirectThread, query.StudentDirect]
	StudentClassThread  cqrs.QueryHandler[query.StudentClassThread, query.StudentClass]

	StaffAttachmentURL   cqrs.QueryHandler[query.StaffAttachmentURL, domain.AttachmentURL]
	StudentAttachmentURL cqrs.QueryHandler[query.StudentAttachmentURL, domain.AttachmentURL]
}

// New builds the application over repo.
func New(repo domain.Repository) *Application {
	service := &support.Service{Repo: repo, Now: time.Now}
	return &Application{
		Commands: Commands{
			PostDirect:       command.PostDirectHandler{Service: service},
			PostAnnouncement: command.PostAnnouncementHandler{Service: service},
			MarkRead:         command.MarkReadHandler{Service: service},

			PostAsStudent:     command.PostAsStudentHandler{Service: service},
			MarkReadAsStudent: command.MarkReadAsStudentHandler{Service: service},

			UploadAttachment: command.UploadAttachmentHandler{Service: service},
		},
		Queries: Queries{
			StaffThreads:      query.StaffThreadsHandler{Service: service},
			StaffDirectThread: query.StaffDirectThreadHandler{Service: service},
			StaffClassThread:  query.StaffClassThreadHandler{Service: service},

			StudentThreads:      query.StudentThreadsHandler{Service: service},
			StudentDirectThread: query.StudentDirectThreadHandler{Service: service},
			StudentClassThread:  query.StudentClassThreadHandler{Service: service},

			StaffAttachmentURL:   query.StaffAttachmentURLHandler{Service: service},
			StudentAttachmentURL: query.StudentAttachmentURLHandler{Service: service},
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

// WithContextEvents reads the flagged attempts a staff member's direct thread
// shows beside its messages. Without it a thread shows none.
func (a *Application) WithContextEvents(events ports.ContextEvents) *Application {
	a.service.Events = events
	return a
}

// WithAttachments keeps messages' files in files and tells audio through
// audio, and makes the housekeeping's two sweeps available. Without it an
// upload and a download answer domain.ErrStorageOff.
func (a *Application) WithAttachments(files ports.AttachmentStore, audio ports.AudioProbe) *Application {
	a.service.Files, a.service.Audio = files, audio
	a.Commands.SweepUnsentAttachments = command.SweepUnsentAttachmentsHandler{Service: a.service}
	a.Commands.SweepAttachmentObjects = command.SweepAttachmentObjectsHandler{Service: a.service, Cursor: &command.SweepCursor{}}
	return a
}

// WithClock reads the time from now, for the URL's expiry and the sweeps'
// grace.
func (a *Application) WithClock(now func() time.Time) *Application {
	a.service.Now = now
	return a
}
