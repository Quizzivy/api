package repositories

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"quizzivy/internal/modules/tests/domain"
	"quizzivy/internal/platform/db"
	"quizzivy/internal/shared/access"
	"quizzivy/internal/shared/opt"
	"quizzivy/internal/shared/provenance"
	"quizzivy/internal/shared/visibility"
)

// testAccess is this module's predicate (D4, T-R7.4): what the caller may do
// with the tests row alias, 'owner', 'edit', 'use' or NULL, for the caller's
// scope.all and user id in placeholders all and user. Every read and write of
// a test, of its draft, its groups and its versions, decides access here and
// nowhere else:
//
//   - read: the owner, any grant, or scope.all
//   - edit: the owner, an edit grant, or scope.all
//   - owner-only: the owner or scope.all
func testAccess(alias string, all, user int) string {
	return visibility.TestAccess(alias, all, user)
}

// The row locks reachTest takes on the test it reads.
const (
	testUnlocked  = ""
	testShared    = "FOR SHARE OF t"
	testExclusive = "FOR UPDATE OF t"
)

// reachedTest is a live test as reachTest read it for a caller: last is its
// last write, whose updated_at is the revision an edit is checked against.
type reachedTest struct {
	owner         string
	access        domain.Access
	status        domain.Status
	last          provenance.LastWrite
	current       int
	lastPublished int
}

// reachTest reads the live test id for scope, under lock, and answers whether
// scope may take action on it: domain.ErrNotFound for a test the scope cannot
// read, exactly as for a missing one, and the refusal of Access.Allows
// (domain.ErrReadOnly, domain.ErrOwnerOnly) for a reader without the right.
// The test it read comes back either way, so a caller may still name it.
func reachTest(ctx context.Context, q db.Querier, scope access.Scope, id string, action domain.Action, lock string) (reachedTest, error) {
	var row reachedTest
	var level, status string
	var last provenance.Row
	err := q.QueryRow(ctx, `SELECT t.owner_id::text, `+testAccess("t", 2, 3)+`, t.status::text, `+provenance.Columns("t")+`,
		       t.current_version, t.last_published_version
		  FROM app.tests t
		 WHERE t.id = $1::uuid AND t.deleted_at IS NULL AND `+testAccess("t", 2, 3)+` IS NOT NULL `+lock,
		id, scope.All, opt.String(scope.UserID)).Scan(append(append([]any{&row.owner, &level, &status}, last.Dest()...), &row.current, &row.lastPublished)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return reachedTest{}, domain.ErrNotFound
	}
	if err != nil {
		return reachedTest{}, fmt.Errorf("tests: reach test: %w", err)
	}
	row.access, row.status, row.last = domain.Access(level), domain.Status(status), last.LastWrite()
	return row, row.access.Allows(action)
}

// readableTests is the condition, over the tests row t, that the caller whose
// scope.all and user id are in placeholders all and user may read it.
func readableTests(all, user int) string {
	return testAccess("t", all, user) + ` IS NOT NULL`
}

// requireVisibleTest is nil when scope may read the live test testID, and
// domain.ErrNotFound otherwise.
func requireVisibleTest(ctx context.Context, q db.Querier, scope access.Scope, testID string) error {
	_, err := reachTest(ctx, q, scope, testID, domain.ActionRead, testUnlocked)
	return err
}

// readerView is what a reader of access sees of t: the reader's access, and
// the grants counted only for its owner.
func readerView(t *domain.Test, level domain.Access) {
	t.Access = level
	if !level.Owner() {
		t.SharedWith = 0
	}
}
