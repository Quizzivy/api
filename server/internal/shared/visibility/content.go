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

// QuestionAccess is the one definition of what a caller may do with a bank
// question (D4, T-R7.4), as an SQL expression over the questions row named by
// alias: 'owner' for its owner and for scope.all; 'edit' when the draft of a
// live test, or a live set, that the caller can edit holds it; 'use' when
// only one the caller can read does; NULL for anyone else. A question is never
// shared on its own: it is read through the test or set that holds it, so the
// grants are those of the caller's on the tests whose draft names it and on
// the sets that hold it. all and user are the placeholders of the caller's
// scope.all and user id.
func QuestionAccess(alias string, all, user int) string {
	return fmt.Sprintf(`(CASE WHEN $%[2]d::boolean OR %[1]s.owner_id = $%[3]d::uuid THEN 'owner'
	  ELSE (SELECT CASE WHEN count(*) = 0 THEN NULL WHEN bool_or(g.access = 'edit') THEN 'edit' ELSE 'use' END
	          FROM app.content_shares g
	         WHERE g.grantee_id = $%[3]d::uuid
	           AND (g.test_id IN (SELECT section.test_id FROM app.test_section_questions sq
	                                JOIN app.test_sections section ON section.id = sq.test_section_id
	                                JOIN app.tests t ON t.id = section.test_id AND t.deleted_at IS NULL
	                               WHERE sq.question_id = %[1]s.id)
	             OR g.question_set_id IN (SELECT i.set_id FROM app.question_set_items i
	                                        JOIN app.question_sets st ON st.id = i.set_id AND st.deleted_at IS NULL
	                                       WHERE i.question_id = %[1]s.id))) END)`, alias, all, user)
}

// SharedTestIDs is the one definition of the tests shared with a teacher, as a
// subquery of test ids for the teacher's id in placeholder $n: those any live
// grant names. A NULL teacher holds none.
func SharedTestIDs(n int) string {
	return fmt.Sprintf(`(SELECT g.test_id FROM app.content_shares g WHERE g.grantee_id = $%d::uuid AND g.test_id IS NOT NULL)`, n)
}
