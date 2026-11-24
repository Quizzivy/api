package visibility

import (
	"fmt"

	"quizzivy/internal/shared/retention"
)

// StudentLike is the boolean SQL expression over the users row named by alias
// that the account passes R2's strict student predicate: its role grants
// learning.take_tests and nothing staff-like (app.student_like_roles).
func StudentLike(alias string) string {
	return alias + `.role_id IN (SELECT r.id FROM app.student_like_roles r)`
}

// MessagingPeerIDs is domain.Eligibility.Peer written in SQL, as a subquery
// of user ids for the staff member's id in placeholder $n: those they hold a
// direct conversation with, and every account they may write to
// (CanMessage); never the staff member, never an anonymised account, none
// for a NULL staff member. No query reads it: since F2 (T-F2.3) the messaging
// module decides a peer from DirectFacts alone, and this subquery only backs
// the parity test that holds the SQL rule (CanMessage, which the Students
// page and the class roster read) to the manager's
// (TestTheEligibilityTableOnTheDatabase). Use CanMessage for whom a staff
// member may write to.
func MessagingPeerIDs(n int) string {
	return fmt.Sprintf(`(SELECT w.id FROM app.users w
	  WHERE $%[1]d::uuid IS NOT NULL AND w.id <> $%[1]d::uuid AND %[2]s
	    AND (EXISTS (SELECT 1 FROM app.conversations v WHERE v.kind = 'direct' AND v.staff_id = $%[1]d::uuid AND v.student_id = w.id)
	         OR %[3]s))`, n, retention.NotAnonymousOf("w"), CanMessage("w", n))
}

// CanMessage is the one definition of whom the staff member in placeholder $n
// may write to in a direct thread, as a boolean SQL expression over the users
// row named by alias: any enabled account that passes R2's strict student
// predicate and is not anonymised, in one of the staff member's classes, in
// another teacher's or in none; never the staff member. It is the messaging
// module's eligibility for a post, which the Students page and the class
// roster show as canMessage, whoever's class scope reaches the student.
func CanMessage(alias string, n int) string {
	return fmt.Sprintf(`(%[1]s.disabled_at IS NULL AND %[2]s AND %[3]s AND $%[4]d::uuid IS NOT NULL AND %[1]s.id <> $%[4]d::uuid)`,
		alias, StudentLike(alias), retention.NotAnonymousOf(alias), n)
}
