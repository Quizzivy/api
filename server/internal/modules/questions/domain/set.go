package domain

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"quizzivy/internal/shared/access"
	"quizzivy/internal/shared/content"
)

// The bounds of a question set (T-R7.2): its name and description, in
// characters after trimming, and how many questions it holds.
const (
	MaxSetName        = 120
	MaxSetDescription = 500
	MaxSetQuestions   = 500
)

var (
	// ErrSetNotFound is a set that is missing, deleted or not readable by the
	// caller, answered alike.
	ErrSetNotFound = errors.New("questions: question set not found")
	// ErrSetReadOnly is a change by a caller who may read the set but not edit it.
	ErrSetReadOnly = errors.New("questions: the set can be read but not changed")
	// ErrSetOwnerOnly is an owner's action by a caller who holds a grant.
	ErrSetOwnerOnly = errors.New("questions: only the set's owner may do this")
	// ErrTestNotFound is a write made for a test (forTestId) the caller cannot
	// read, answered as a missing test.
	ErrTestNotFound = errors.New("questions: test not found for the write")
	// ErrTestReadOnly is a write made for a test (forTestId) the caller reads
	// through a Can use grant: SHARE_READ_ONLY.
	ErrTestReadOnly = errors.New("questions: the test can be read but not changed")
	// ErrReadOnly is a change to a question the caller reads, through a test or
	// a set shared with them, but may not edit: SHARE_READ_ONLY.
	ErrReadOnly = errors.New("questions: the question can be read but not changed")
	// ErrOwnerOnly is an owner's action on a question (delete, duplicate) by a
	// caller who reads it through a share: SHARE_OWNER_ONLY.
	ErrOwnerOnly = errors.New("questions: only the question's owner may do this")
	// ErrEditContextRequired is an edit, by a caller who is not the question's
	// owner but may edit a test or a set that holds it, made outside that
	// context: EDIT_CONTEXT_REQUIRED.
	ErrEditContextRequired = errors.New("questions: a shared question is edited in the context of its test or set")
	// ErrSetFull is an add that would leave a set holding more than
	// MaxSetQuestions.
	ErrSetFull = errors.New("questions: the set would hold too many questions")
)

// Access is what a caller may do with a question set: Owner for its owner and
// scope.all, Edit and Use for a share's two levels (D4).
type Access string

const (
	AccessOwner Access = "owner"
	AccessEdit  Access = "edit"
	AccessUse   Access = "use"
)

// Owner reports whether a is the owner's standing.
func (a Access) Owner() bool { return a == AccessOwner }

// WriterAccess is what the writer of req may do with the question it wrote:
// Owner for a question of their own bank and under scope.all, Edit for one
// made for a test of another owner's that they may edit (forTestId).
func WriterAccess(req WriteRequest) Access {
	if req.All || req.OwnerID == "" || req.OwnerID == req.ActorID {
		return AccessOwner
	}
	return AccessEdit
}

// SetAction is something done to a question set, which Access allows or not.
type SetAction int

const (
	// SetRead reads the set and its questions.
	SetRead SetAction = iota
	// SetChange renames the set, changes its description or removes questions.
	SetChange
	// SetAdd adds questions; only the owner adds (O-R7.4).
	SetAdd
	// SetDelete deletes the set and its grants.
	SetDelete
)

// Allows is nil when a holds the right to act, ErrSetReadOnly for a change by a
// reader who cannot edit, and ErrSetOwnerOnly for an owner's action by anyone
// else.
func (a Access) Allows(action SetAction) error {
	switch action {
	case SetRead:
		return nil
	case SetChange:
		if a == AccessOwner || a == AccessEdit {
			return nil
		}
		return ErrSetReadOnly
	default:
		if a == AccessOwner {
			return nil
		}
		return ErrSetOwnerOnly
	}
}

// QuestionSet is a named, ordered selection of its owner's standalone bank
// questions (DG-90, interim). It holds references, not copies. Access is the
// reader's; SharedWith counts the grants to it.
type QuestionSet struct {
	ID            string
	OwnerID       string
	OwnerName     string
	Name          string
	Description   *string
	Access        Access
	QuestionCount int
	SharedWith    int
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// QuestionsNotFoundError names the ids of a set write that are not live
// standalone questions of the set owner's bank, in the order given, so another
// teacher's ids answer as missing ones do; and those of a copy that the copier
// cannot read, or that the set copied does not hold. It is an ErrNotFound.
type QuestionsNotFoundError struct{ IDs []string }

func (e *QuestionsNotFoundError) Error() string {
	return fmt.Sprintf("questions: %d question(s) not found for the set", len(e.IDs))
}

func (e *QuestionsNotFoundError) Is(target error) bool { return target == ErrNotFound }

// SetFields is a set's name and description as a write gives them. On an
// update a nil Name is unchanged, and Description changes only with
// DescriptionSet.
type SetFields struct {
	Name           *string
	Description    *string
	DescriptionSet bool
}

// Clean returns the fields trimmed and composed to NFC, an empty description as
// none, or a ValidationError naming each field outside its bounds.
func (f SetFields) Clean() (SetFields, error) {
	invalid := &ValidationError{}
	out := SetFields{DescriptionSet: f.DescriptionSet}
	if f.Name != nil {
		name := content.NFC(strings.TrimSpace(*f.Name))
		switch n := utf8.RuneCountInString(name); {
		case n == 0:
			invalid.AddIn("name", "Tên bộ câu hỏi không được để trống.", "The set's name cannot be empty.")
		case n > MaxSetName:
			invalid.AddIn("name", fmt.Sprintf("Tên bộ câu hỏi không được dài quá %d ký tự.", MaxSetName),
				fmt.Sprintf("The set's name cannot be longer than %d characters.", MaxSetName))
		}
		out.Name = &name
	}
	if f.Description != nil {
		description := content.NFC(strings.TrimSpace(*f.Description))
		if utf8.RuneCountInString(description) > MaxSetDescription {
			invalid.AddIn("description", fmt.Sprintf("Mô tả không được dài quá %d ký tự.", MaxSetDescription),
				fmt.Sprintf("The description cannot be longer than %d characters.", MaxSetDescription))
		}
		if description != "" {
			out.Description = &description
		}
	}
	if err := invalid.OrNil(); err != nil {
		return SetFields{}, err
	}
	return out, nil
}

// SetQuestionIDs refuses a list of questions a set write cannot take: none, more
// than MaxSetQuestions, or one id twice.
func SetQuestionIDs(ids []string) error {
	invalid := &ValidationError{}
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if seen[id] {
			invalid.AddIn("questionIds", "Mỗi câu hỏi chỉ được nêu một lần.", "Each question can be named only once.")
			break
		}
		seen[id] = true
	}
	switch {
	case len(ids) == 0:
		invalid.AddIn("questionIds", "Cần chọn ít nhất một câu hỏi.", "Choose at least one question.")
	case len(ids) > MaxSetQuestions:
		invalid.AddIn("questionIds", fmt.Sprintf("Một bộ câu hỏi chứa tối đa %d câu.", MaxSetQuestions),
			fmt.Sprintf("A set holds at most %d questions.", MaxSetQuestions))
	}
	if err := invalid.OrNil(); err != nil {
		return err
	}
	return nil
}

// SetCapacity is ErrSetFull when adding adding questions to a set that holds
// held would leave it holding more than MaxSetQuestions.
func SetCapacity(held, adding int) error {
	if held+adding <= MaxSetQuestions {
		return nil
	}
	return ErrSetFull
}

// SetWrite is a write to the set ID by ActorID, with All for scope.all.
type SetWrite struct {
	ID        string
	ActorID   string
	All       bool
	Now       time.Time
	IP        string
	UserAgent string
}

// Scope is the scope the write reads under.
func (w SetWrite) Scope() access.Scope { return access.Scope{UserID: w.ActorID, All: w.All} }

// SetRepository persists question sets and their items.
type SetRepository interface {
	// ListSets returns the sets scope.UserID owns, most recently changed first.
	ListSets(ctx context.Context, scope access.Scope) ([]QuestionSet, error)
	// GetSet returns a live set the scope can read, or ErrSetNotFound.
	GetSet(ctx context.Context, scope access.Scope, id string) (QuestionSet, error)
	// CreateSet makes a set owned by the actor holding questionIDs in order.
	CreateSet(ctx context.Context, w SetWrite, fields SetFields, questionIDs []string) (QuestionSet, error)
	// UpdateSet changes the set's name or description.
	UpdateSet(ctx context.Context, w SetWrite, fields SetFields) (QuestionSet, error)
	// DeleteSet soft-deletes the set and deletes its grants.
	DeleteSet(ctx context.Context, w SetWrite) error
	// AddToSet appends questionIDs the set does not hold yet, in order.
	AddToSet(ctx context.Context, w SetWrite, questionIDs []string) (QuestionSet, error)
	// RemoveFromSet takes questionIDs out of the set; one it does not hold is ignored.
	RemoveFromSet(ctx context.Context, w SetWrite, questionIDs []string) (QuestionSet, error)
	// CopySet copies a set the actor can read into the actor's own content:
	// the whole set as a new one when questionIDs is nil, otherwise just those
	// of its questions, into the actor's bank.
	CopySet(ctx context.Context, w SetWrite, questionIDs []string) (SetCopy, error)
}

// WriteContexts writes a bank edit made in the context of a set (setId). The
// test a write is made for (forTestId) is the tests module's, reached through
// ports.EditableTests, and so is an edit in a test's context
// (editTestQuestion).
type WriteContexts interface {
	// UpdateInSet edits the question in.ID in the set setID's context, under
	// the copy-on-write rule (QuestionUse.Forks), and answers the question as
	// saved, the copy on a fork. ErrSetNotFound when the actor cannot read the
	// set or it does not hold the question; the refusal of Access.Allows for a
	// reader who may not change it.
	UpdateInSet(ctx context.Context, in WriteInput, setID string) (Question, error)
}

// IsSetRefusal reports whether err is a reader's refusal to change a set
// (Access.Allows): ErrSetReadOnly or ErrSetOwnerOnly.
func IsSetRefusal(err error) bool {
	return errors.Is(err, ErrSetReadOnly) || errors.Is(err, ErrSetOwnerOnly)
}
