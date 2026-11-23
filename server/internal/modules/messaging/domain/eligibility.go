package domain

// DirectFacts is what the database holds about a staff member and the account
// a direct thread names as its student.
type DirectFacts struct {
	// Exists is false for an id no account has.
	Exists bool
	// Self is the staff member naming themselves.
	Self bool
	// Conversation is a direct thread between the two that exists.
	Conversation bool
	// Enabled is the account not being disabled.
	Enabled bool
	// Student is the account passing R2's strict student predicate
	// (app.student_like_roles).
	Student bool
	// Anonymised is the account having been anonymised (D16): its threads are
	// gone with it, and it is no one's to write to.
	Anonymised bool
}

// ChannelFacts is what the database holds about a class's channel and the
// caller.
type ChannelFacts struct {
	// InScope is the class existing and being the caller's to read: its
	// teacher, or scope.all.
	InScope bool
	// Archived is the class being archived.
	Archived bool
	// MayWrite is the caller holding teaching.classes.write.
	MayWrite bool
}

// Eligibility decides who reads and who posts in a thread (D17, DG-08,
// O-R7.8). It holds no state: every decision is a function of the facts the
// repository read.
type Eligibility struct{}

// Peer reports whether the account is a messaging peer of the staff member
// (F2): a thread exists between them, or the account is an enabled student,
// in one of the staff member's classes, in another teacher's or in none.
// Never the staff member themselves, never an anonymised account. The
// student predicate binds only the open half: a thread stays readable after
// its student is disabled or given a staff role, while a staff account, or a
// disabled student, with no thread resolves as no peer at all (404, as a
// missing id). scope.all plays no part: every staff member who opens the
// workspace gets the same rule, and a peer opens only the caller's own thread
// with the student, never another staff member's (D17).
func (Eligibility) Peer(f DirectFacts) bool {
	return f.Exists && !f.Self && !f.Anonymised && (f.Conversation || (f.Enabled && f.Student))
}

// ReadDirect lets the staff member read their thread with a peer:
// ErrNotFound for anyone else.
func (e Eligibility) ReadDirect(f DirectFacts) error {
	if !e.Peer(f) {
		return ErrNotFound
	}
	return nil
}

// PostDirect lets the staff member post to a peer who is enabled and still a
// student, whatever classes they are in. ErrNotFound for an account that is
// no peer; ErrNotAllowed for a peer whose account is disabled or no longer a
// student, whose thread stays readable.
func (e Eligibility) PostDirect(f DirectFacts) error {
	if err := e.ReadDirect(f); err != nil {
		return err
	}
	if !f.Enabled || !f.Student {
		return ErrNotAllowed
	}
	return nil
}

// CanPost reports whether PostDirect would let the post through.
func (e Eligibility) CanPost(f DirectFacts) bool { return e.PostDirect(f) == nil }

// ReadChannel lets anyone whose class scope reaches the class read its
// channel, archived or not: ErrNotFound for anyone else.
func (Eligibility) ReadChannel(f ChannelFacts) error {
	if !f.InScope {
		return ErrNotFound
	}
	return nil
}

// PostChannel lets a reader of the channel who may write to classes post an
// announcement, while the class is not archived: ErrForbidden without
// teaching.classes.write, ErrClassArchived for an archived class.
func (e Eligibility) PostChannel(f ChannelFacts) error {
	if err := e.ReadChannel(f); err != nil {
		return err
	}
	if !f.MayWrite {
		return ErrForbidden
	}
	if f.Archived {
		return ErrClassArchived
	}
	return nil
}
