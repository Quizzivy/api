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
	// Tied is a row in app.messaging_ties: the student is, or has been, in a
	// class of the staff member's (T-R7.9).
	Tied bool
	// Created is the staff member having created the account
	// (users.created_by).
	Created bool
	// Enabled is the account not being disabled.
	Enabled bool
	// Student is the account passing R2's strict student predicate
	// (app.student_like_roles).
	Student bool
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

// Peer reports whether the account is a messaging peer of the staff member:
// a thread exists between them, the staff member taught the student, or the
// staff member created the account and it is a student. The student predicate
// binds only the last: a thread or a tie stays readable after its student is
// given a staff role, but an account the caller created and that is not a
// student, a colleague's, resolves as no peer at all (404, as a missing id).
// scope.all does not widen it, so a student the caller never taught stays out
// of reach, an admin's included.
func (Eligibility) Peer(f DirectFacts) bool {
	return f.Exists && !f.Self && (f.Conversation || f.Tied || (f.Created && f.Student))
}

// ReadDirect lets the staff member read their thread with a peer, which a
// thread outlives the tie it started from: ErrNotFound for anyone else.
func (e Eligibility) ReadDirect(f DirectFacts) error {
	if !e.Peer(f) {
		return ErrNotFound
	}
	return nil
}

// PostDirect lets the staff member post to a peer who is enabled and still a
// student. A thread that exists stays open after the student leaves the
// class: the tie decides who may start a thread, not who may continue one.
// ErrNotFound for an account that is no peer; ErrNotAllowed for a peer whose
// account is disabled or no longer a student, whose thread stays readable.
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
