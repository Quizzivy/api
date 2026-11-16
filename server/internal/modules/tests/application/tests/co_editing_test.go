//go:build integration

package application_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"quizzivy/internal/core/adapters"
	mediarepo "quizzivy/internal/modules/media/repositories"
	questionsapp "quizzivy/internal/modules/questions/application"
	questionscommand "quizzivy/internal/modules/questions/application/command"
	questionsdomain "quizzivy/internal/modules/questions/domain"
	questionsrepo "quizzivy/internal/modules/questions/repositories"
	"quizzivy/internal/modules/tests/application"
	"quizzivy/internal/modules/tests/domain"
	"quizzivy/internal/modules/tests/repositories"
	"quizzivy/internal/platform/db"
	"quizzivy/internal/shared/access"
	"quizzivy/internal/shared/provenance"
)

// coEditWorld is A's test, holding A's question, shared with B as Can edit,
// written through a pool: each write is its own transaction, as two sessions'
// autosaves are, so each one moves the test's updated_at.
type coEditWorld struct {
	pool           *pgxpool.Pool
	tests          *repositories.Postgres
	questions      *questionsapp.Application
	a, b           string
	scopeA, scopeB access.Scope
	testA          string
	questionA      string
}

func newCoEditWorld(t *testing.T) *coEditWorld {
	t.Helper()
	ctx := context.Background()
	pool := newPool(t)
	w := &coEditWorld{pool: pool}
	w.a, w.b = coEditor(t, pool, "Cô A"), coEditor(t, pool, "Thầy B")
	t.Cleanup(func() { removeCoEditors(t, pool, w.a, w.b) })
	w.scopeA, w.scopeB = access.Scope{UserID: w.a}, access.Scope{UserID: w.b}
	w.tests = repositories.NewPostgres(db.NewContext(pool), adapters.GroupQuestions{}, mediarepo.NewPostgres(db.NewContext(pool))).WithGroupQuestions(adapters.GroupQuestions{})
	w.questions = questionsapp.New(questionsrepo.NewPostgres(db.NewContext(pool)), mediaKinds{pool}).
		WithTests(adapters.EditableTests{Tests: application.New(w.tests).Queries.Ownership})
	if err := pool.QueryRow(ctx, `INSERT INTO app.questions (type, prompt, points, created_by, owner_id, tags)
		VALUES ('short_answer', 'Câu của A', 1, $1, $1, '{}') RETURNING id::text`, w.a).Scan(&w.questionA); err != nil {
		t.Fatal(err)
	}

	created, err := w.tests.Create(ctx, domain.CreateInput{Title: "A's shared test", ActorID: w.a, Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	w.testA = created.ID
	if _, err := w.tests.Update(ctx, domain.UpdateRequest{ID: w.testA, ActorID: w.a, Now: time.Now(), Scope: w.scopeA,
		Input: domain.UpdateInput{ExpectedUpdatedAt: created.UpdatedAt, SetSections: true, Sections: []domain.SectionInput{{Title: "Part 1", QuestionIDs: []string{w.questionA}}}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO app.content_shares (kind, test_id, grantee_id, access, granted_by)
		VALUES ('test', $1::uuid, $2::uuid, 'edit', $3::uuid)`, w.testA, w.b, w.a); err != nil {
		t.Fatal(err)
	}
	return w
}

func coEditor(t *testing.T, pool *pgxpool.Pool, name string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO app.users (email, full_name, role_id) VALUES ($1, $2, (SELECT id FROM app.roles WHERE builtin_key = 'teacher')) RETURNING id::text`,
		"co-edit-"+uuid.NewString()+"@example.test", name).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// removeCoEditors deletes what the co-editors made, the tests before the bank
// questions their drafts hold, and then the co-editors.
func removeCoEditors(t *testing.T, pool *pgxpool.Pool, ids ...string) {
	t.Helper()
	for _, sql := range []string{
		`DELETE FROM app.audit_log WHERE actor_user_id = ANY($1::uuid[])`,
		`DELETE FROM app.tests WHERE owner_id = ANY($1::uuid[]) OR created_by = ANY($1::uuid[])`,
		`DELETE FROM app.questions WHERE owner_id = ANY($1::uuid[]) OR created_by = ANY($1::uuid[])`,
		`DELETE FROM app.users WHERE id = ANY($1::uuid[])`,
	} {
		if _, err := pool.Exec(context.Background(), sql, ids); err != nil {
			t.Errorf("clean up %q: %v", sql, err)
		}
	}
}

func (w *coEditWorld) read(t *testing.T, scope access.Scope) domain.Test {
	t.Helper()
	test, err := w.tests.Get(context.Background(), scope, w.testA)
	if err != nil {
		t.Fatal(err)
	}
	return test
}

func (w *coEditWorld) retitle(scope access.Scope, actorID, title string, expected time.Time) (domain.Test, error) {
	return w.tests.Update(context.Background(), domain.UpdateRequest{ID: w.testA, ActorID: actorID, Now: time.Now(), Scope: scope,
		Input: domain.UpdateInput{ExpectedUpdatedAt: expected, Title: &title}})
}

func (w *coEditWorld) lastWriter(t *testing.T) (by *string, title string) {
	t.Helper()
	if err := w.pool.QueryRow(context.Background(), `SELECT updated_by::text, title FROM app.tests WHERE id = $1`, w.testA).Scan(&by, &title); err != nil {
		t.Fatal(err)
	}
	return by, title
}

// expectStaleNaming holds err to a stale write naming who saved last, by id
// and name, and when.
func expectStaleNaming(t *testing.T, name string, err error, id, person string, at time.Time) {
	t.Helper()
	if !errors.Is(err, domain.ErrStaleWrite) {
		t.Fatalf("%s: %v, want a stale write", name, err)
	}
	last, named := provenance.Of(err)
	switch {
	case !named:
		t.Errorf("%s names no last write", name)
	case id == "" && last.By != nil:
		t.Errorf("%s names %+v, want no one", name, *last.By)
	case id != "" && (last.By == nil || last.By.ID != id || last.By.Name != person):
		t.Errorf("%s names %+v, want %s (%s)", name, last.By, person, id)
	case !last.At.Truncate(time.Microsecond).Equal(at.Truncate(time.Microsecond)):
		t.Errorf("%s names a write at %s, want %s", name, last.At, at)
	}
}

// TestTwoSessionsAutosavingOneTestTellTheSecondWhoSavedFirst: A and B open
// A's test at one revision. A saves first; B's save on that revision is
// refused naming A and the time of A's save, and writes nothing. Once B has
// reloaded, B's save is recorded as B's, and A's next save on the old
// revision names B.
func TestTwoSessionsAutosavingOneTestTellTheSecondWhoSavedFirst(t *testing.T) {
	w := newCoEditWorld(t)
	opened := w.read(t, w.scopeA).UpdatedAt
	if seen := w.read(t, w.scopeB).UpdatedAt; !seen.Equal(opened) {
		t.Fatalf("A and B read different revisions: %s and %s", opened, seen)
	}

	savedA, err := w.retitle(w.scopeA, w.a, "A's title", opened)
	if err != nil {
		t.Fatal(err)
	}
	if by, _ := w.lastWriter(t); by == nil || *by != w.a {
		t.Errorf("A's save is recorded as %v's", by)
	}
	_, err = w.retitle(w.scopeB, w.b, "B's title", opened)
	expectStaleNaming(t, "B's save on the revision A replaced", err, w.a, "Cô A", savedA.UpdatedAt)
	if by, title := w.lastWriter(t); by == nil || *by != w.a || title != "A's title" {
		t.Errorf("B's refused save wrote: updated_by %v, title %q", by, title)
	}

	savedB, err := w.retitle(w.scopeB, w.b, "B's title", savedA.UpdatedAt)
	if err != nil {
		t.Fatalf("B's save after reloading: %v", err)
	}
	if by, title := w.lastWriter(t); by == nil || *by != w.b || title != "B's title" {
		t.Errorf("B's save is recorded as updated_by %v, title %q", by, title)
	}
	_, err = w.retitle(w.scopeA, w.a, "A's second title", savedA.UpdatedAt)
	expectStaleNaming(t, "A's save on the revision B replaced", err, w.b, "Thầy B", savedB.UpdatedAt)
}

// TestARowLastWrittenByNoOneNamesNoOne: a test the R6 binary last wrote, or
// whose last writer's account is gone, has no updated_by, and a stale write
// on it names no one but still says when it changed.
func TestARowLastWrittenByNoOneNamesNoOne(t *testing.T) {
	w := newCoEditWorld(t)
	ctx := context.Background()
	if _, err := w.pool.Exec(ctx, `UPDATE app.tests SET updated_by = NULL WHERE id = $1`, w.testA); err != nil {
		t.Fatal(err)
	}
	current := w.read(t, w.scopeA).UpdatedAt
	_, err := w.retitle(w.scopeB, w.b, "B's title", current.Add(-time.Minute))
	expectStaleNaming(t, "a save on a test no one is recorded on", err, "", "", current)
}

// TestAGranteesQuestionMadeForTheTestPassesTheDraftsOwnerCheck: R2's rule
// that a draft only holds questions its test's owner owns is unchanged. A
// question B makes for A's test (forTestId) is A's, so B's autosave may put
// it in the draft; one B makes in B's own bank is refused as a missing one
// is.
func TestAGranteesQuestionMadeForTheTestPassesTheDraftsOwnerCheck(t *testing.T) {
	w := newCoEditWorld(t)
	ctx := context.Background()
	input := func(prompt string) questionsdomain.WriteRequest {
		return questionsdomain.WriteRequest{ActorID: w.b, Input: questionsdomain.Input{
			Type: questionsdomain.ShortAnswer, Prompt: prompt, Points: "1.00", Tags: []string{}}}
	}
	made, err := w.questions.Commands.Create.Handle(ctx, questionscommand.Create{Request: input("Câu B làm cho đề của A"), ForTestID: w.testA})
	if err != nil {
		t.Fatal(err)
	}
	var owner, author, writer string
	if err := w.pool.QueryRow(ctx, `SELECT owner_id::text, created_by::text, updated_by::text FROM app.questions WHERE id = $1`, made.ID).Scan(&owner, &author, &writer); err != nil {
		t.Fatal(err)
	}
	if owner != w.a || author != w.b || writer != w.b {
		t.Errorf("B's question for A's test: owner %s, created_by %s, updated_by %s; want A, B, B", owner, author, writer)
	}
	own, err := w.questions.Commands.Create.Handle(ctx, questionscommand.Create{Request: input("Câu trong ngân hàng của B")})
	if err != nil {
		t.Fatal(err)
	}

	outline := func(extra string) error {
		_, err := w.tests.Update(ctx, domain.UpdateRequest{ID: w.testA, ActorID: w.b, Now: time.Now(), Scope: w.scopeB,
			Input: domain.UpdateInput{ExpectedUpdatedAt: w.read(t, w.scopeB).UpdatedAt, SetSections: true,
				Sections: []domain.SectionInput{{Title: "Part 1", QuestionIDs: []string{w.questionA, extra}}}}})
		return err
	}
	if err := outline(own.ID); !errors.Is(err, domain.ErrUnknownQuestion) {
		t.Errorf("B's own question in A's draft: %v, want the unknown question a missing id gets", err)
	}
	if err := outline(uuid.NewString()); !errors.Is(err, domain.ErrUnknownQuestion) {
		t.Errorf("a missing question in A's draft: %v, want the unknown question", err)
	}
	if err := outline(made.ID); err != nil {
		t.Fatalf("B's question made for A's test, in A's draft: %v", err)
	}
	draft := w.read(t, w.scopeA)
	if len(draft.Sections) != 1 || len(draft.Sections[0].QuestionIDs) != 2 || draft.Sections[0].QuestionIDs[1] != made.ID {
		t.Errorf("A's draft after B's save: %+v", draft.Sections)
	}
}

// TestEveryTestsWriteRecordsItsWriter walks the writes of a test's draft by
// its owner and by an edit grantee, each of which records its actor as the
// test's last writer, and a refused stale group mutation names that writer.
func TestEveryTestsWriteRecordsItsWriter(t *testing.T) {
	ctx := context.Background()
	w := newShareWorld(t, levelEdit)
	writer := func() string {
		t.Helper()
		var by *string
		if err := w.tx.QueryRow(ctx, `SELECT updated_by::text FROM app.tests WHERE id = $1`, w.testA).Scan(&by); err != nil {
			t.Fatal(err)
		}
		if by == nil {
			return ""
		}
		return *by
	}
	expectWriter := func(name, want string, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := writer(); got != want {
			t.Errorf("%s is recorded as %q's, want %q's", name, got, want)
		}
	}
	revision := func() time.Time { return w.current(t, w.testA).UpdatedAt }
	if got := writer(); got != w.a {
		t.Errorf("A's own world setup is recorded as %q's", got)
	}

	group, err := w.groups.Create(ctx, domain.CreateGroupInput{Bundle: storedGroupFixture(t, ""), OwnerSectionID: &w.sectionA,
		ExpectedTestUpdatedAt: revision(), ActorID: w.b, Now: time.Now(), Scope: w.scopeB, Grants: bothKeys})
	expectWriter("B's group made in the test", w.b, err)
	title := "A's title"
	_, err = w.tests.Update(ctx, domain.UpdateRequest{ID: w.testA, ActorID: w.a, Now: time.Now(), Scope: w.scopeA,
		Input: domain.UpdateInput{ExpectedUpdatedAt: revision(), Title: &title}})
	expectWriter("A's autosave", w.a, err)
	_, err = w.groups.Update(ctx, domain.UpdateGroupInput{Bundle: group.Bundle, GroupMutation: domain.GroupMutation{ID: group.Bundle.Group.ID,
		ExpectedRevision: group.Revision, ExpectedTestUpdatedAt: revision(), ActorID: w.b, Now: time.Now(), Scope: w.scopeB, Grants: bothKeys}})
	expectWriter("B's group update", w.b, err)

	// Stale group mutations name the test's last writer, B, and its revision.
	_, err = w.groups.Update(ctx, domain.UpdateGroupInput{Bundle: group.Bundle, GroupMutation: domain.GroupMutation{ID: group.Bundle.Group.ID,
		ExpectedRevision: group.Revision, ExpectedTestUpdatedAt: revision(), ActorID: w.a, Now: time.Now(), Scope: w.scopeA, Grants: bothKeys}})
	expectStaleNaming(t, "A's update on the group revision B replaced", err, w.b, "Scope fixture", revision())
	err = w.groups.RemoveFromSection(ctx, domain.GroupMutation{ID: w.sectionGroupA.Bundle.Group.ID, ExpectedRevision: w.sectionGroupA.Revision,
		ExpectedTestUpdatedAt: revision().Add(-time.Minute), ActorID: w.a, Now: time.Now(), Scope: w.scopeA, Grants: bothKeys})
	expectStaleNaming(t, "A's removal on a test revision B replaced", err, w.b, "Scope fixture", revision())
	sectionGroup := w.sectionGroupA.Bundle.Group.ID
	_, err = w.groups.MoveQuestion(ctx, domain.MoveQuestionInput{TestID: w.testA, QuestionID: w.questionA,
		ExpectedTestUpdatedAt: revision(), To: domain.MoveTarget{SectionID: w.sectionA, GroupID: &sectionGroup},
		ExpectedGroups: []domain.GroupRevision{{GroupID: sectionGroup, Revision: w.sectionGroupA.Revision - 1}}, ActorID: w.a, Now: time.Now(), Scope: w.scopeA, Grants: bothKeys})
	expectStaleNaming(t, "A's move on a group revision since replaced", err, w.b, "Scope fixture", revision())
	_, err = w.groups.SetArchived(ctx, domain.GroupMutation{ID: w.bankGroupA.Bundle.Group.ID, ExpectedRevision: w.bankGroupA.Revision - 1,
		ActorID: w.a, Now: time.Now(), Scope: w.scopeA, Grants: bothKeys}, true)
	expectStaleNaming(t, "A's archive of a bank group on an old revision", err, "", "", w.bankGroupA.UpdatedAt)

	// Version management by the grantee, and a publish.
	_, err = w.tests.SetCurrentVersion(ctx, domain.VersionRequest{Request: w.request(w.scopeB, w.b), Version: 1, ExpectedUpdatedAt: revision()}, time.Now())
	expectWriter("B making version 1 the default", w.b, err)
	_, err = w.tests.Update(ctx, domain.UpdateRequest{ID: w.testA, ActorID: w.a, Now: time.Now(), Scope: w.scopeA,
		Input: domain.UpdateInput{ExpectedUpdatedAt: revision(), Title: &title}})
	expectWriter("A's next autosave", w.a, err)
	_, err = w.tests.Publish(ctx, domain.PublishRequest{TestID: w.testA, ActorID: w.b, Scope: w.scopeB}, time.Now(), func(domain.DraftContent) error { return nil })
	expectWriter("B's publish", w.b, err)
	_, err = w.tests.SetCurrentVersion(ctx, domain.VersionRequest{Request: w.request(w.scopeA, w.a), Version: 1, ExpectedUpdatedAt: revision()}, time.Now())
	expectWriter("A making version 1 the default again", w.a, err)
	_, err = w.tests.CreateDraftFromVersion(ctx, domain.VersionRequest{Request: w.request(w.scopeB, w.b), Version: 1, ExpectedUpdatedAt: revision()}, time.Now())
	expectWriter("B restoring version 1 as the draft", w.b, err)
	expectWriter("A deleting version 2", w.a, w.tests.DeleteVersion(ctx, domain.VersionRequest{Request: w.request(w.scopeA, w.a), Version: 2}, time.Now()))

	copied, err := w.tests.Duplicate(ctx, domain.DuplicateInput{ID: w.testA, ActorID: w.a, Now: time.Now(), Scope: w.scopeA})
	if err != nil {
		t.Fatal(err)
	}
	var copyWriter string
	if err := w.tx.QueryRow(ctx, `SELECT updated_by::text FROM app.tests WHERE id = $1`, copied.ID).Scan(&copyWriter); err != nil || copyWriter != w.a {
		t.Errorf("A's duplicate is recorded as %q's (%v)", copyWriter, err)
	}
}

// TestABankGroupCopiedIntoASharedDraftBelongsToTheTestsOwner: B, with Can
// edit, copies a group of B's own bank into A's test. The copy and every
// question in it are A's, with B as their author, as R2's rule for the
// draft's questions wants.
func TestABankGroupCopiedIntoASharedDraftBelongsToTheTestsOwner(t *testing.T) {
	ctx := context.Background()
	w := newShareWorld(t, levelEdit)
	bankB, err := w.groups.Create(ctx, domain.CreateGroupInput{Bundle: storedGroupFixture(t, ""), ActorID: w.b, Now: time.Now(), Scope: w.scopeB, Grants: bothKeys})
	if err != nil {
		t.Fatal(err)
	}
	copied, err := w.groups.Copy(ctx, domain.CopyGroupInput{SourceID: bankB.Bundle.Group.ID, ExpectedSourceRevision: bankB.Revision, OwnerSectionID: &w.sectionA,
		ExpectedTestUpdatedAt: w.current(t, w.testA).UpdatedAt, ActorID: w.b, Now: time.Now(), Scope: w.scopeB, Grants: bothKeys})
	if err != nil {
		t.Fatal(err)
	}
	var groupOwner, groupAuthor string
	if err := w.tx.QueryRow(ctx, `SELECT owner_id::text, created_by::text FROM app.question_groups WHERE id = $1`, copied.Bundle.Group.ID).Scan(&groupOwner, &groupAuthor); err != nil {
		t.Fatal(err)
	}
	if groupOwner != w.a || groupAuthor != w.b {
		t.Errorf("the copied group: owner %s, author %s; want A and B", groupOwner, groupAuthor)
	}
	rows, err := w.tx.Query(ctx, `SELECT owner_id::text, created_by::text, updated_by::text FROM app.questions WHERE context_group_id = $1`, copied.Bundle.Group.ID)
	if err != nil {
		t.Fatal(err)
	}
	members := 0
	for rows.Next() {
		var owner, author, writer string
		if err := rows.Scan(&owner, &author, &writer); err != nil {
			t.Fatal(err)
		}
		members++
		if owner != w.a || author != w.b || writer != w.b {
			t.Errorf("a member of the copy: owner %s, created_by %s, updated_by %s; want A, B, B", owner, author, writer)
		}
	}
	rows.Close()
	if members != len(bankB.Bundle.Group.Members) {
		t.Errorf("the copy holds %d members, want %d", members, len(bankB.Bundle.Group.Members))
	}

	// A stale source is refused naming no one: a bank group records no writer.
	_, err = w.groups.Copy(ctx, domain.CopyGroupInput{SourceID: bankB.Bundle.Group.ID, ExpectedSourceRevision: bankB.Revision - 1, OwnerSectionID: &w.sectionA,
		ExpectedTestUpdatedAt: w.current(t, w.testA).UpdatedAt, ActorID: w.b, Now: time.Now(), Scope: w.scopeB, Grants: bothKeys})
	expectStaleNaming(t, "a copy of a source since changed", err, "", "", bankB.UpdatedAt)
}
