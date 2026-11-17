package command

import (
	"context"
	notificationscommand "quizzivy/internal/modules/notifications/application/command"
	notificationsdomain "quizzivy/internal/modules/notifications/domain"
	"quizzivy/internal/modules/tests/application/internal/support"
	"quizzivy/internal/modules/tests/domain"
	"quizzivy/internal/shared/content"
	"quizzivy/internal/shared/cqrs"
)

// Publish freezes the draft as a new version, for the test's owner or an edit
// grantee (D4). The publisher is recorded as the version's published_by and
// the owner stays its author; when an edit grantee publishes, the owner is told
// in the app (test.published_by_collaborator, with no switch: DG-90).
type Publish struct {
	Request domain.PublishRequest
}

type PublishHandler struct {
	*support.Publisher
}

func (p PublishHandler) Handle(ctx context.Context, cmd Publish) (domain.Version, error) {
	var c content.Composer
	note := c.Optional("changeNote", cmd.Request.ChangeNote, domain.MaxChangeNote)
	if err := c.Err(); err != nil {
		return domain.Version{}, err
	}
	cmd.Request.ChangeNote = domain.Publishing.ChangeNote(note)
	version, err := p.Repo.Publish(ctx, cmd.Request, p.Now(), domain.Publishing.Validate)
	if err != nil {
		return domain.Version{}, err
	}
	p.announce(ctx, cmd.Request, version)
	return version, nil
}

// announce tells the owner of a test a co-editor published, after the commit.
// The publisher's standing is read again under its own scope: an owner's and
// scope.all's own publish tell no one, and a grant revoked since the commit
// tells no one either.
func (p PublishHandler) announce(ctx context.Context, req domain.PublishRequest, version domain.Version) {
	cqrs.Announce(ctx, p.Notifier, p.Logger, "test published by a collaborator", func(ctx context.Context) ([]notificationscommand.Notify, error) {
		test, err := p.Repo.Ownership(ctx, req.Scope, req.TestID)
		if err != nil || test.Access != domain.AccessEdit || test.OwnerID == req.ActorID {
			return nil, nil
		}
		return []notificationscommand.Notify{{
			UserID: test.OwnerID,
			Kind:   notificationsdomain.TestPublishedByCollaborator,
			Params: notificationsdomain.PublishedByCollaborator{
				EditorName: version.PublishedBy, Title: test.Title, Version: version.Version,
			},
			Target:    &notificationsdomain.Target{Route: notificationsdomain.RouteTest, TestID: test.TestID},
			DedupeKey: notificationsdomain.CollaboratorPublishedKey(test.TestID, version.Version),
			Merge:     notificationsdomain.Replace,
		}}, nil
	})
}
