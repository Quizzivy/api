package domain

import "time"

// KindColleague is a thread between two staff members (F2, T-F2.4): one per
// pair, addressed on either side by the other member, its pair held in
// app.conversations' colleague_low_id and colleague_high_id, the smaller
// uuid first.
const KindColleague Kind = "colleague"

// ColleagueFacts is what the database holds about a staff member and the
// account a colleague thread names as their colleague.
type ColleagueFacts struct {
	// Exists is false for an id no account has.
	Exists bool
	// Self is the staff member naming themselves.
	Self bool
	// Conversation is a colleague thread between the two that exists.
	Conversation bool
	// Enabled is the account not being disabled.
	Enabled bool
	// Workspace is the account's role opening the teacher workspace
	// (limits.TeacherRole): an account that opens only the Admin console
	// cannot open Messages, so it is no colleague.
	Workspace bool
	// Student is the account passing R2's strict student predicate
	// (app.student_like_roles).
	Student bool
	// Anonymised is the account having been anonymised (D16).
	Anonymised bool
}

// Colleague reports whether the account is a messaging colleague of the
// staff member (F2): a colleague thread exists between them, or the account
// is enabled, opens the teacher workspace and is not a student. Never the
// staff member themselves, never an anonymised account. As Peer does for a
// student, the open half binds only a thread that does not exist yet: a
// thread stays readable after its colleague is disabled or loses the
// workspace, while such an account with no thread resolves as no colleague
// at all (404, as a missing id). scope.all plays no part, and a colleague
// opens only the caller's own thread with them, never another pair's (D17).
func (Eligibility) Colleague(f ColleagueFacts) bool {
	return f.Exists && !f.Self && !f.Anonymised && (f.Conversation || f.open())
}

func (f ColleagueFacts) open() bool { return f.Enabled && f.Workspace && !f.Student }

// ReadColleague lets the staff member read their thread with a colleague:
// ErrNotFound for anyone else.
func (e Eligibility) ReadColleague(f ColleagueFacts) error {
	if !e.Colleague(f) {
		return ErrNotFound
	}
	return nil
}

// PostColleague lets the staff member post to a colleague who is enabled,
// still opens the teacher workspace and is not a student. ErrNotFound for an
// account that is no colleague; ErrNotAllowed for a colleague who no longer
// qualifies, whose thread stays readable.
func (e Eligibility) PostColleague(f ColleagueFacts) error {
	if err := e.ReadColleague(f); err != nil {
		return err
	}
	if !f.open() {
		return ErrNotAllowed
	}
	return nil
}

// CanPostColleague reports whether PostColleague would let the post through.
func (e Eligibility) CanPostColleague(f ColleagueFacts) bool { return e.PostColleague(f) == nil }

// ColleaguePeer is the other staff member of a colleague thread, as its
// header and Details show them: name, photo, the role's stored name and its
// built-in key (nil for a custom role), whether the account is disabled and
// whether a post would be taken. Never an email.
type ColleaguePeer struct {
	ID             string
	Name           string
	AvatarKey      *string
	AvatarURL      *string
	RoleName       string
	RoleBuiltinKey *string
	Disabled       bool
	CanPost        bool
}

// ColleagueThread is a row of the Colleagues tab: the caller's thread with a
// colleague, or, for a search, a Candidate the caller may write to and has
// no thread with.
type ColleagueThread struct {
	ColleagueID    string
	Title          string
	AvatarKey      *string
	AvatarURL      *string
	RoleName       string
	RoleBuiltinKey *string
	Candidate      bool
	Disabled       bool
	LastMessageAt  *time.Time
	Preview        *Preview
	Unread         int
}

// ColleagueFilter is what the Colleagues tab reads: the caller, and a search
// on the colleague's names. scope.all widens nothing here.
type ColleagueFilter struct {
	ActorID string
	Query   string
}

// OtherOf is the member of the pair low, high who is not reader: the
// colleague a reader's thread is addressed by.
func OtherOf(reader, low, high string) string {
	if reader == low {
		return high
	}
	return low
}
