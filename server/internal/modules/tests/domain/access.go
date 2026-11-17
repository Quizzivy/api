package domain

import "errors"

var (
	// ErrReadOnly is a change by a caller who reads the test through a Can use
	// grant: SHARE_READ_ONLY.
	ErrReadOnly = errors.New("tests: the test can be read but not changed")
	// ErrOwnerOnly is an owner's action by a caller who reads the test through
	// a grant: SHARE_OWNER_ONLY.
	ErrOwnerOnly = errors.New("tests: only the test's owner may do this")
)

// Access is what a caller may do with a test (D4): Owner for its owner and
// for scope.all, Edit and Use for a share's two levels.
type Access string

const (
	AccessOwner Access = "owner"
	AccessEdit  Access = "edit"
	AccessUse   Access = "use"
)

// Action is something done to a test, which Access allows or not.
type Action int

const (
	// ActionRead reads the test: detail, builder, versions, preview, diff,
	// its groups, and assigning or copying a version.
	ActionRead Action = iota
	// ActionEdit changes the draft: autosave, questions and groups, restore
	// as draft, make default, and publish.
	ActionEdit
	// ActionOwn is the owner's alone: delete, archive and restore, share,
	// duplicate by reference and delete a version.
	ActionOwn
)

// Allows is nil when a holds the right to act; ErrReadOnly for a change by a
// Can use reader, and ErrOwnerOnly for an owner's action by any other reader.
func (a Access) Allows(action Action) error {
	switch action {
	case ActionRead:
		return nil
	case ActionEdit:
		if a == AccessOwner || a == AccessEdit {
			return nil
		}
		return ErrReadOnly
	default:
		if a == AccessOwner {
			return nil
		}
		return ErrOwnerOnly
	}
}

// Owner reports whether a is the owner's standing.
func (a Access) Owner() bool { return a == AccessOwner }
