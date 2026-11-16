// Package domain is the sharing context (D4): a grant of one teacher's test or
// question set to a colleague as Can use or Can edit, the directory a sharer
// picks colleagues from, and Shared with me, what others gave the reader. A
// grant stores no owner: the owner is always the resource's current owner.
package domain

import (
	"errors"
	"strings"
	"time"

	"quizzivy/internal/shared/access"
	"quizzivy/internal/shared/content"
)

// Kind is what a grant gives, as the contract's ShareKind and the kind column
// of app.content_shares.
type Kind string

const (
	KindTest        Kind = "test"
	KindQuestionSet Kind = "question_set"
)

// Valid reports whether k is a kind a grant can give.
func (k Kind) Valid() bool { return k == KindTest || k == KindQuestionSet }

// Access is a grant's level, as the contract's ShareAccess.
type Access string

const (
	AccessUse  Access = "use"
	AccessEdit Access = "edit"
)

// Valid reports whether a is a level a grant can hold.
func (a Access) Valid() bool { return a == AccessUse || a == AccessEdit }

// The bounds of the sharing operations: the directory's rows, the recipients
// of one share and the sharer's message, in characters after trimming.
const (
	MaxDirectory = 10
	MaxGrantees  = 20
	MaxMessage   = 500
)

var (
	// ErrNotFound is a resource or a grant that is missing, deleted or not
	// readable by the caller, answered alike so ids stay unguessable.
	ErrNotFound = errors.New("sharing: not found")
	// ErrOwnerOnly is an owner's action by a caller who holds a grant: only the
	// content's owner and scope.all share it, change or stop a grant (D4).
	ErrOwnerOnly = errors.New("sharing: only the content's owner may do this")
	// ErrInvalid is a kind or an access level the sharing context does not know.
	ErrInvalid = errors.New("sharing: unknown kind or access")
)

// Resource is the test or question set a grant names, as the caller's scope
// reads it: its title or name and its current owner.
type Resource struct {
	Kind    Kind
	ID      string
	Name    string
	OwnerID string
}

// Standing is what a caller is to a resource.
type Standing int

const (
	// Stranger cannot read the resource.
	Stranger Standing = iota
	// Grantee reads it through a grant.
	Grantee
	// Owner is its owner, or holds scope.all.
	Owner
)

// StandingOf is the caller's standing to res, which the caller's scope could
// read (readable) or could not, given whether the caller holds a live grant to
// it (holds).
func StandingOf(scope access.Scope, res Resource, readable, holds bool) Standing {
	switch {
	case readable && (scope.All || res.OwnerID == scope.UserID):
		return Owner
	case readable || holds:
		return Grantee
	}
	return Stranger
}

// Manage is nil when a caller of standing s may read and change who the
// resource is shared with; ErrOwnerOnly for a grantee, and ErrNotFound for
// anyone else, as for a missing resource.
func (s Standing) Manage() error {
	switch s {
	case Owner:
		return nil
	case Grantee:
		return ErrOwnerOnly
	}
	return ErrNotFound
}

// Person is a user as the share dialog shows them. AvatarURL is signed from
// AvatarKey when a store is configured.
type Person struct {
	ID          string
	FullName    string
	DisplayName string
	Email       string
	AvatarKey   *string
	AvatarURL   *string
	Disabled    bool
}

// PersonRef is a person by id and display name.
type PersonRef struct {
	ID   string
	Name string
}

// Grant is one recipient's access to a resource.
type Grant struct {
	ID         string
	Kind       Kind
	ResourceID string
	Grantee    Person
	Access     Access
	GrantedAt  time.Time
	GrantedBy  PersonRef
}

// Share is a grant as its row stores it, which an update or a removal is
// decided on.
type Share struct {
	ID         string
	Kind       Kind
	ResourceID string
	GranteeID  string
	Access     Access
}

// SkipCode is why a named recipient got no grant, as the contract's
// ShareSkipCode.
type SkipCode string

const (
	SkipSelf          SkipCode = "SELF"
	SkipAlreadyShared SkipCode = "ALREADY_SHARED"
	SkipNotATeacher   SkipCode = "NOT_A_TEACHER"
	SkipDisabled      SkipCode = "DISABLED"
)

// Candidate is a user a sharer named, as the store found them: whether the
// account exists, opens the teacher workspace, is disabled, and already holds
// a grant to the resource.
type Candidate struct {
	UserID   string
	Exists   bool
	Teacher  bool
	Disabled bool
	Granted  bool
}

// Skip is why c gets no grant to a resource owned by ownerID that actorID
// shares, and false when c gets one. The owner and the sharer are SELF, an
// account that is missing or lacks the teacher workspace is NOT_A_TEACHER, a
// holder of a grant is ALREADY_SHARED and a disabled account DISABLED, in
// that order.
func Skip(c Candidate, ownerID, actorID string) (SkipCode, bool) {
	switch {
	case c.UserID == ownerID || c.UserID == actorID:
		return SkipSelf, true
	case !c.Exists || !c.Teacher:
		return SkipNotATeacher, true
	case c.Granted:
		return SkipAlreadyShared, true
	case c.Disabled:
		return SkipDisabled, true
	}
	return "", false
}

// Skipped is a named recipient who got no grant, and why.
type Skipped struct {
	UserID string
	Code   SkipCode
}

// Granted is what a share made: the grants in the order the recipients were
// named, and the recipients skipped.
type Granted struct {
	Grants  []Grant
	Skipped []Skipped
}

// Transition is what setting a grant from access from to access to does: true
// for a change that is written and audited, false for none. A level the
// context does not know is ErrInvalid.
func Transition(from, to Access) (bool, error) {
	if !to.Valid() {
		return false, ErrInvalid
	}
	return from != to, nil
}

// CleanMessage is the sharer's message trimmed and composed to NFC, nil when
// it is absent or empty.
func CleanMessage(message *string) *string {
	if message == nil {
		return nil
	}
	cleaned := content.NFC(strings.TrimSpace(*message))
	if cleaned == "" {
		return nil
	}
	return &cleaned
}

// Write is a change made by ActorID, with All for scope.all, and what its
// audit row records.
type Write struct {
	ActorID   string
	All       bool
	Now       time.Time
	IP        string
	UserAgent string
}

// Scope is the scope the write reads under.
func (w Write) Scope() access.Scope { return access.Scope{UserID: w.ActorID, All: w.All} }

// GrantRequest shares Resource with GranteeIDs at Access.
type GrantRequest struct {
	Write
	Resource   Resource
	GranteeIDs []string
	Access     Access
	Message    *string
}

// SharedOwner is the owner of something shared with the reader.
type SharedOwner struct {
	ID        string
	Name      string
	AvatarKey *string
	AvatarURL *string
	Disabled  bool
}

// SharedItem is one row of Shared with me. QuestionCount is a set's live
// questions, nil for a test.
type SharedItem struct {
	ShareID       string
	Kind          Kind
	ResourceID    string
	Name          string
	Owner         SharedOwner
	Access        Access
	SharedAt      time.Time
	QuestionCount *int
}

// Counts are the reader's live grants per tab of Shared with me.
type Counts struct {
	All         int
	Test        int
	QuestionSet int
}

// SharedFilter is one page of Shared with me for UserID: of Kind (every kind
// when empty), matching Query on the name and the owner's name.
type SharedFilter struct {
	UserID string
	Kind   Kind
	Query  string
	Page   int
	Limit  int
}
