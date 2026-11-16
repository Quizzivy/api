package visibility

import "fmt"

// TestAccess is the one definition of what a caller may do with a test (D4),
// as an SQL expression over the tests row named by alias: 'owner' for its
// owner and for scope.all, the level of the caller's grant in
// app.content_shares ('edit' or 'use'), and NULL for anyone who cannot read
// it. all and user are the placeholders of the caller's scope.all and user id.
// A NULL user owns nothing and holds no grant.
func TestAccess(alias string, all, user int) string {
	return contentAccess(alias, "test_id", all, user)
}

// SetAccess is TestAccess for a question set, over the question_sets row named
// by alias.
func SetAccess(alias string, all, user int) string {
	return contentAccess(alias, "question_set_id", all, user)
}

func contentAccess(alias, column string, all, user int) string {
	return fmt.Sprintf(`(CASE WHEN $%[3]d::boolean OR %[1]s.owner_id = $%[4]d::uuid THEN 'owner'
	  ELSE (SELECT g.access FROM app.content_shares g WHERE g.%[2]s = %[1]s.id AND g.grantee_id = $%[4]d::uuid) END)`,
		alias, column, all, user)
}

// SharedTestIDs is the one definition of the tests shared with a teacher, as a
// subquery of test ids for the teacher's id in placeholder $n: those any live
// grant names. A NULL teacher holds none.
func SharedTestIDs(n int) string {
	return fmt.Sprintf(`(SELECT g.test_id FROM app.content_shares g WHERE g.grantee_id = $%d::uuid AND g.test_id IS NOT NULL)`, n)
}
