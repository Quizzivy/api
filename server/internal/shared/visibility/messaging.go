package visibility

import "fmt"

// StudentLike is the boolean SQL expression over the users row named by alias
// that the account passes R2's strict student predicate: its role grants
// learning.take_tests and nothing staff-like (app.student_like_roles).
func StudentLike(alias string) string {
	return alias + `.role_id IN (SELECT r.id FROM app.student_like_roles r)`
}

// MessagingPeerIDs is the one definition of the accounts a staff member may
// name as the student of a direct thread (T-R7.10a, O-R7.8), as a subquery of
// user ids for the staff member's id in placeholder $n: those they hold a
// direct conversation with, have taught in a class of theirs, now or before
// (app.messaging_ties), or created (users.created_by) when the account is a
// student (StudentLike): a staff account the caller created is no peer.
// scope.all does not widen it: a direct thread is its two participants' only
// (D17, DG-08). A NULL staff member has none. It is the messaging module's
// domain.Eligibility.Peer.
func MessagingPeerIDs(n int) string {
	return fmt.Sprintf(`(SELECT v.student_id FROM app.conversations v WHERE v.kind = 'direct' AND v.staff_id = $%[1]d::uuid
	 UNION ALL SELECT t.student_id FROM app.messaging_ties t WHERE t.staff_id = $%[1]d::uuid
	 UNION ALL SELECT w.id FROM app.users w WHERE w.created_by = $%[1]d::uuid AND %[2]s)`, n, StudentLike("w"))
}

// CanMessage is the one definition of whom the staff member in placeholder $n
// may write to in a direct thread, as a boolean SQL expression over the users
// row named by alias: a messaging peer (MessagingPeerIDs), enabled, and a
// student by R2's strict predicate. It is the messaging module's eligibility
// for a post, which the Students page and the class roster show as canMessage.
func CanMessage(alias string, n int) string {
	return fmt.Sprintf(`(%[1]s.disabled_at IS NULL AND %[2]s AND %[1]s.id IS DISTINCT FROM $%[3]d::uuid AND %[1]s.id IN %[4]s)`,
		alias, StudentLike(alias), n, MessagingPeerIDs(n))
}
