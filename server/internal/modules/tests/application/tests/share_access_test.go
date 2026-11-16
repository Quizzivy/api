//go:build integration

package application_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	mediadomain "quizzivy/internal/modules/media/domain"
	notificationscommand "quizzivy/internal/modules/notifications/application/command"
	notificationsdomain "quizzivy/internal/modules/notifications/domain"
	"quizzivy/internal/modules/tests/application"
	"quizzivy/internal/modules/tests/application/command"
	"quizzivy/internal/modules/tests/domain"
	"quizzivy/internal/shared/access"
	"quizzivy/internal/shared/cqrs"
)

// The levels a teacher can hold to another teacher's test (D4): none, a
// Can use grant, a Can edit grant, and a grant since revoked.
const (
	levelNone    = "none"
	levelUse     = "use"
	levelEdit    = "edit"
	levelRevoked = "revoked"
)

// shareWorld is the scope world with A's test shared to B at a level.
type shareWorld struct {
	*scopeWorld
	level string
}

func newShareWorld(t *testing.T, level string) *shareWorld {
	t.Helper()
	w := &shareWorld{scopeWorld: newScopeWorld(t), level: level}
	switch level {
	case levelUse, levelEdit:
		w.grant(t, level)
	case levelRevoked:
		w.grant(t, levelEdit)
		w.revoke(t)
	}
	return w
}

func (w *shareWorld) grant(t *testing.T, level string) {
	t.Helper()
	if _, err := w.tx.Exec(context.Background(), `INSERT INTO app.content_shares (kind, test_id, grantee_id, access, granted_by)
		VALUES ('test', $1::uuid, $2::uuid, $3, $4::uuid)`, w.testA, w.b, level, w.a); err != nil {
		t.Fatal(err)
	}
}

func (w *shareWorld) revoke(t *testing.T) {
	t.Helper()
	if _, err := w.tx.Exec(context.Background(), `DELETE FROM app.content_shares WHERE test_id = $1::uuid AND grantee_id = $2::uuid`, w.testA, w.b); err != nil {
		t.Fatal(err)
	}
}

// actionClass is which right an action needs, as the contract's
// ContentAccess table names them.
type actionClass int

const (
	reads actionClass = iota
	edits
	owns
)

// want is what B is answered at the world's level for an action of class c:
// nil for an action B may take, the refusal of a reader without the right,
// and domain.ErrNotFound (missing, for publish) for a caller who cannot read
// the test.
func (w *shareWorld) want(c actionClass, missing error) error {
	switch {
	case w.level == levelNone || w.level == levelRevoked:
		return missing
	case c == owns:
		return domain.ErrOwnerOnly
	case c == edits && w.level == levelUse:
		return domain.ErrReadOnly
	}
	return nil
}

func (w *shareWorld) check(t *testing.T, name string, c actionClass, got error, missing ...error) {
	t.Helper()
	absent := domain.ErrNotFound
	if len(missing) > 0 {
		absent = missing[0]
	}
	want := w.want(c, absent)
	if want == nil && got != nil || want != nil && !errors.Is(got, want) {
		t.Errorf("%s at %s: %v, want %v", name, w.level, got, want)
	}
}

// TestEachActionOnASharedTestAnswersAtEachLevelOfTheGrant holds every read and
// write of the tests module to the ContentAccess table: B, with no grant, a
// Can use grant, a Can edit grant and a revoked one, reads, edits and takes
// the owner's actions on A's test, its versions and its test-owned group.
func TestEachActionOnASharedTestAnswersAtEachLevelOfTheGrant(t *testing.T) {
	for _, level := range []string{levelNone, levelUse, levelEdit, levelRevoked} {
		t.Run(level, func(t *testing.T) {
			ctx := context.Background()
			w := newShareWorld(t, level)
			now := time.Now()

			got, err := w.tests.Get(ctx, w.scopeB, w.testA)
			w.check(t, "get", reads, err)
			if err == nil && (string(got.Access) != level || got.OwnerID != w.a || got.SharedWith != 0) {
				t.Errorf("B's test reads access %q, owner %s, sharedWith %d; want %s, A and 0", got.Access, got.OwnerID, got.SharedWith, level)
			}
			owned, err := w.tests.Ownership(ctx, w.scopeB, w.testA)
			w.check(t, "ownership", reads, err)
			if err == nil && (string(owned.Access) != level || owned.OwnerID != w.a) {
				t.Errorf("B's ownership read: %+v", owned)
			}
			_, err = w.tests.ListVersions(ctx, w.scopeB, w.testA)
			w.check(t, "versions", reads, err)
			_, err = w.tests.Preview(ctx, w.scopeB, w.testA, 0)
			w.check(t, "preview", reads, err)
			_, err = w.groups.Get(ctx, w.scopeB, w.sectionGroupA.Bundle.Group.ID)
			w.check(t, "the test's own group", reads, err)
			_, err = w.groups.Get(ctx, w.scopeB, w.bankGroupA.Bundle.Group.ID)
			expectNotFound(t, "A's bank group, which no grant opens", err)

			listed, _, err := w.tests.List(ctx, domain.ListInput{Limit: 100, Scope: w.scopeB, IncludeShared: true})
			if err != nil {
				t.Fatal(err)
			}
			at := slices.IndexFunc(listed, func(test domain.Test) bool { return test.ID == w.testA })
			if (at >= 0) != (w.want(reads, domain.ErrNotFound) == nil) {
				t.Errorf("include=shared lists A's test: %v", at >= 0)
			}
			if at >= 0 && (string(listed[at].Access) != level || listed[at].SharedWith != 0) {
				t.Errorf("the shared row reads access %q, sharedWith %d", listed[at].Access, listed[at].SharedWith)
			}
			own, _, err := w.tests.List(ctx, domain.ListInput{Limit: 100, Scope: w.scopeB})
			if err != nil {
				t.Fatal(err)
			}
			if slices.ContainsFunc(own, func(test domain.Test) bool { return test.ID == w.testA }) {
				t.Error("B's own list holds A's test")
			}

			// The owner's actions, before any edit moves the revision.
			current := w.current(t, w.testA)
			archived := domain.Archived
			_, err = w.tests.Update(ctx, domain.UpdateRequest{ID: w.testA, ActorID: w.b, Now: now, Scope: w.scopeB,
				Input: domain.UpdateInput{ExpectedUpdatedAt: current.UpdatedAt, Status: &archived}})
			w.check(t, "archive", owns, err)
			_, err = w.tests.Duplicate(ctx, domain.DuplicateInput{ID: w.testA, ActorID: w.b, Now: now, Scope: w.scopeB})
			w.check(t, "duplicate", owns, err)
			w.check(t, "delete", owns, w.tests.Delete(ctx, w.request(w.scopeB, w.b), now))
			w.check(t, "delete a version", owns, w.tests.DeleteVersion(ctx, domain.VersionRequest{Request: w.request(w.scopeB, w.b), Version: 1}, now))

			// The edits, each on the revision the last one left.
			title := "B's title"
			_, err = w.tests.Update(ctx, domain.UpdateRequest{ID: w.testA, ActorID: w.b, Now: now, Scope: w.scopeB,
				Input: domain.UpdateInput{ExpectedUpdatedAt: w.current(t, w.testA).UpdatedAt, Title: &title}})
			w.check(t, "autosave", edits, err)
			_, err = w.groups.Create(ctx, domain.CreateGroupInput{Bundle: storedGroupFixture(t, ""), OwnerSectionID: &w.sectionA,
				ExpectedTestUpdatedAt: w.current(t, w.testA).UpdatedAt, ActorID: w.b, Now: now, Scope: w.scopeB, Grants: bothKeys})
			w.check(t, "a group made in the test", edits, err)
			err = w.groups.RemoveFromSection(ctx, domain.GroupMutation{ID: w.sectionGroupA.Bundle.Group.ID, ExpectedRevision: w.sectionGroupA.Revision,
				ExpectedTestUpdatedAt: w.current(t, w.testA).UpdatedAt, ActorID: w.b, Now: now, Scope: w.scopeB, Grants: bothKeys})
			w.check(t, "the test's group removed", edits, err)
			_, err = w.tests.Publish(ctx, domain.PublishRequest{TestID: w.testA, ActorID: w.b, Scope: w.scopeB}, now, func(domain.DraftContent) error { return nil })
			w.check(t, "publish", edits, err, domain.ErrDraftNotFound)
			_, err = w.tests.SetCurrentVersion(ctx, domain.VersionRequest{Request: w.request(w.scopeB, w.b), Version: 1, ExpectedUpdatedAt: w.current(t, w.testA).UpdatedAt}, now)
			w.check(t, "make default", edits, err)
			_, err = w.tests.CreateDraftFromVersion(ctx, domain.VersionRequest{Request: w.request(w.scopeB, w.b), Version: 1, ExpectedUpdatedAt: w.current(t, w.testA).UpdatedAt}, now)
			w.check(t, "restore as draft", edits, err)

			var owner, author string
			if err := w.tx.QueryRow(ctx, `SELECT owner_id::text, created_by::text FROM app.tests WHERE id = $1`, w.testA).Scan(&owner, &author); err != nil {
				t.Fatal(err)
			}
			if owner != w.a || author != w.a {
				t.Errorf("A's test is owned by %s and authored by %s after B's work, want A for both", owner, author)
			}
		})
	}
}

// TestAMoveAnswersTheTestAsItsCallerReadsIt: a question moved into A's
// test-owned group answers the test with the mover's own access, and the
// grants counted only for A.
func TestAMoveAnswersTheTestAsItsCallerReadsIt(t *testing.T) {
	for _, level := range []string{levelNone, levelUse, levelEdit, levelRevoked} {
		t.Run(level, func(t *testing.T) {
			ctx := context.Background()
			w := newShareWorld(t, level)
			into := func(scope access.Scope, actorID, question string) (domain.MoveQuestionResult, error) {
				group := w.sectionGroupA.Bundle.Group.ID
				var revision int64
				if err := w.tx.QueryRow(ctx, `SELECT revision FROM app.question_groups WHERE id = $1`, group).Scan(&revision); err != nil {
					t.Fatal(err)
				}
				return w.groups.MoveQuestion(ctx, domain.MoveQuestionInput{TestID: w.testA, QuestionID: question,
					ExpectedTestUpdatedAt: w.current(t, w.testA).UpdatedAt, To: domain.MoveTarget{SectionID: w.sectionA, GroupID: &group},
					ExpectedGroups: []domain.GroupRevision{{GroupID: group, Revision: revision}}, ActorID: actorID, Now: time.Now(), Scope: scope, Grants: bothKeys})
			}
			moved, err := into(w.scopeB, w.b, w.questionA)
			w.check(t, "a move into the test's group", edits, err)
			if err == nil && (moved.Test.Access != domain.AccessEdit || moved.Test.OwnerID != w.a || moved.Test.SharedWith != 0) {
				t.Errorf("B's move answers access %q, owner %s, sharedWith %d; want edit, A and 0", moved.Test.Access, moved.Test.OwnerID, moved.Test.SharedWith)
			}

			// A moves what is left: B's copy, within the group, or A's
			// standalone question into it.
			question := w.questionA
			if err == nil {
				question = moved.QuestionID
			}
			moved, err = into(w.scopeA, w.a, question)
			if err != nil {
				t.Fatal(err)
			}
			shared := 0
			if level == levelUse || level == levelEdit {
				shared = 1
			}
			if moved.Test.Access != domain.AccessOwner || moved.Test.SharedWith != shared {
				t.Errorf("A's move answers access %q, sharedWith %d; want owner and %d", moved.Test.Access, moved.Test.SharedWith, shared)
			}
		})
	}
}

// TestAnEditGranteesGroupWriteIsHeldToTheirOwnMediaUntilTRSevenSeven pins a
// known gap T-R7.7 closes: the media binding check still reads assets with the
// writer's own scope, so B, with Can edit, is refused the owner's audio as if
// it were missing, on a resave of the test's group and on a restore of a
// version that holds it. T-R7.7's "readable asset" (an asset referenced by
// content the actor can read) turns both into successes; flip them there.
func TestAnEditGranteesGroupWriteIsHeldToTheirOwnMediaUntilTRSevenSeven(t *testing.T) {
	ctx := context.Background()
	w := newShareWorld(t, levelEdit)
	audio, err := w.groups.Create(ctx, domain.CreateGroupInput{Bundle: storedGroupFixture(t, storedGroupAsset(t, w.tx, w.a, "audio")), OwnerSectionID: &w.sectionA,
		ExpectedTestUpdatedAt: w.current(t, w.testA).UpdatedAt, ActorID: w.a, Now: time.Now(), Scope: w.scopeA, Grants: bothKeys})
	if err != nil {
		t.Fatal(err)
	}
	_, err = w.groups.Update(ctx, domain.UpdateGroupInput{Bundle: audio.Bundle, GroupMutation: domain.GroupMutation{ID: audio.Bundle.Group.ID,
		ExpectedRevision: audio.Revision, ExpectedTestUpdatedAt: w.current(t, w.testA).UpdatedAt, ActorID: w.b, Now: time.Now(), Scope: w.scopeB, Grants: bothKeys}})
	if !errors.Is(err, mediadomain.ErrNotFound) {
		t.Errorf("B resaving the test's audio group: %v, want the media not-found until T-R7.7", err)
	}
	if _, err := w.groups.Update(ctx, domain.UpdateGroupInput{Bundle: audio.Bundle, GroupMutation: domain.GroupMutation{ID: audio.Bundle.Group.ID,
		ExpectedRevision: audio.Revision, ExpectedTestUpdatedAt: w.current(t, w.testA).UpdatedAt, ActorID: w.a, Now: time.Now(), Scope: w.scopeA, Grants: bothKeys}}); err != nil {
		t.Errorf("A resaving the same group: %v", err)
	}

	version, err := w.tests.Publish(ctx, domain.PublishRequest{TestID: w.testA, ActorID: w.a, Scope: w.scopeA}, time.Now(), func(domain.DraftContent) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	restore := func(scope access.Scope, actorID string) error {
		_, err := w.tests.CreateDraftFromVersion(ctx, domain.VersionRequest{Request: w.request(scope, actorID), Version: version.Version,
			ExpectedUpdatedAt: w.current(t, w.testA).UpdatedAt}, time.Now())
		return err
	}
	if err := restore(w.scopeB, w.b); !errors.Is(err, mediadomain.ErrNotFound) {
		t.Errorf("B restoring a version with A's audio group: %v, want the media not-found until T-R7.7", err)
	}
	if err := restore(w.scopeA, w.a); err != nil {
		t.Errorf("A restoring the same version: %v", err)
	}
}

// TestAStaleRevisionIsRefusedOnlyAfterTheReadersRight shows that a reader
// without the right is refused whatever revision they send.
func TestAStaleRevisionIsRefusedOnlyAfterTheReadersRight(t *testing.T) {
	ctx := context.Background()
	w := newShareWorld(t, levelUse)
	_, err := w.tests.Update(ctx, domain.UpdateRequest{ID: w.testA, ActorID: w.b, Now: time.Now(), Scope: w.scopeB,
		Input: domain.UpdateInput{ExpectedUpdatedAt: time.Now().Add(-time.Hour)}})
	if !errors.Is(err, domain.ErrReadOnly) {
		t.Errorf("a Can use autosave on a stale revision: %v, want read only", err)
	}
}

// TestAnEditGranteesPublishIsTheirsAndTellsTheOwner: the version records B as
// its publisher, A stays the test's author, and A is told in the app.
func TestAnEditGranteesPublishIsTheirsAndTellsTheOwner(t *testing.T) {
	ctx := context.Background()
	w := newShareWorld(t, levelEdit)
	told := &notices{}
	app := application.New(w.tests).WithNotifier(told, nil)

	version, err := app.Commands.Publish.Handle(ctx, command.Publish{Request: domain.PublishRequest{TestID: w.testA, ActorID: w.b, Scope: w.scopeB}})
	if err != nil {
		t.Fatal(err)
	}
	var publisher string
	if err := w.tx.QueryRow(ctx, `SELECT published_by::text FROM app.test_versions WHERE id = $1`, version.ID).Scan(&publisher); err != nil {
		t.Fatal(err)
	}
	if publisher != w.b {
		t.Errorf("B's version was published by %s", publisher)
	}
	if len(told.sent) != 1 {
		t.Fatalf("A was told %d times, want once: %+v", len(told.sent), told.sent)
	}
	notice := told.sent[0]
	params, _ := notice.Params.(notificationsdomain.PublishedByCollaborator)
	if notice.UserID != w.a || notice.Kind != notificationsdomain.TestPublishedByCollaborator ||
		params.Title != "A's test" || params.Version != version.Version || params.EditorName == "" ||
		notice.Target == nil || notice.Target.Route != notificationsdomain.RouteTest || notice.Target.TestID != w.testA {
		t.Errorf("A was told %+v", notice)
	}

	if _, err := app.Commands.Publish.Handle(ctx, command.Publish{Request: domain.PublishRequest{TestID: w.testA, ActorID: w.a, Scope: w.scopeA}}); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Commands.Publish.Handle(ctx, command.Publish{Request: domain.PublishRequest{TestID: w.testA, ActorID: w.admin, Scope: w.anyone}}); err != nil {
		t.Fatal(err)
	}
	if len(told.sent) != 1 {
		t.Errorf("the owner's and scope.all's own publish told someone: %+v", told.sent[1:])
	}
}

// TestRevokingMidSessionRefusesTheNextAutosave: B edits A's test, A stops
// sharing it, and B's next autosave answers as for a missing test.
func TestRevokingMidSessionRefusesTheNextAutosave(t *testing.T) {
	ctx := context.Background()
	w := newShareWorld(t, levelEdit)
	title := "B's first save"
	saved, err := w.tests.Update(ctx, domain.UpdateRequest{ID: w.testA, ActorID: w.b, Now: time.Now(), Scope: w.scopeB,
		Input: domain.UpdateInput{ExpectedUpdatedAt: w.current(t, w.testA).UpdatedAt, Title: &title}})
	if err != nil {
		t.Fatal(err)
	}
	if saved.Access != domain.AccessEdit {
		t.Errorf("B's save reads access %q", saved.Access)
	}
	w.revoke(t)
	title = "B's second save"
	_, err = w.tests.Update(ctx, domain.UpdateRequest{ID: w.testA, ActorID: w.b, Now: time.Now(), Scope: w.scopeB,
		Input: domain.UpdateInput{ExpectedUpdatedAt: saved.UpdatedAt, Title: &title}})
	expectNotFound(t, "the autosave after the revocation", err)
}

type notices struct{ sent []notificationscommand.Notify }

func (n *notices) Handle(_ context.Context, cmd notificationscommand.Notify) (cqrs.Nothing, error) {
	n.sent = append(n.sent, cmd)
	return cqrs.Nothing{}, nil
}
