package domain

import (
	"slices"
	"strings"
)

// ContextKind is what a bank question is edited in the context of: a test's
// draft (editTestQuestion) or a question set (updateQuestion?setId).
type ContextKind string

const (
	ContextTest ContextKind = "test"
	ContextSet  ContextKind = "set"
)

// EditContext names the test or set an edit is made in. Its zero value is an
// edit outside any context: the question's owner's, or scope.all's, in the bank.
type EditContext struct {
	Kind ContextKind
	ID   string
}

// Within reports whether the edit names a context.
func (c EditContext) Within() bool { return c.Kind != "" && c.ID != "" }

// QuestionUse is what a copy-on-write decision reads about a bank question,
// under the row lock LockForDraftUse takes: its owner, whether it is a member
// of a test-owned group, the media it binds, and how many live drafts and live
// sets other than the edit's context use it. Media are lower-case ids.
type QuestionUse struct {
	ID          string
	OwnerID     string
	GroupMember bool
	Media       []string
	OtherDrafts int
	OtherSets   int
}

// Forks is the copy-on-write rule (D4, T-R7.6). An edit by actorID in context
// makes a copy of the question, owned by its owner and created by the editor,
// when the editor is not the question's owner, the edit is made in a test or a
// set the editor can edit, and some other draft or some other set uses the
// question; otherwise the question is edited in place. The owner's edits never
// fork, and change every draft that uses the question, as before; nor does an
// edit outside a context, which only the owner and scope.all may make. A group
// member never forks: it belongs to its test-owned group, which no other draft
// or set can use.
func (u QuestionUse) Forks(actorID string, within EditContext) bool {
	switch {
	case u.GroupMember, !within.Within(), actorID == u.OwnerID:
		return false
	}
	return u.OtherDrafts > 0 || u.OtherSets > 0
}

// Keeps reports whether assetID is one of the media the question already
// binds. An edit made in a context may keep them whoever makes it: reading the
// question through that context authorized them, as a duplicate's source
// media are (WriteRequest.SourceMedia). Any other asset binds under the
// editor's own scope.
func (u QuestionUse) Keeps(assetID string) bool {
	return slices.Contains(u.Media, strings.ToLower(assetID))
}
