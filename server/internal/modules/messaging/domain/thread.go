// Package domain is the messaging context (D17): one announcement channel per
// class, direct threads between a staff member and a student, and colleague
// threads between two staff members (F2); who may start one, who reads it
// and who posts in it. Messages are never edited or
// deleted, and nothing here is audited: the append-only messages table is the
// record, readable only by its participants.
package domain

import (
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

// Kind is a conversation's kind, as app.conversations.kind and the contract's
// StaffThreadRef.kind.
type Kind string

const (
	// KindClass is a class's announcement channel.
	KindClass Kind = "class"
	// KindDirect is a thread between one staff member and one student.
	KindDirect Kind = "direct"
)

// Tab is which rows the staff Messages list shows.
type Tab string

const (
	TabAll     Tab = "all"
	TabDirect  Tab = "direct"
	TabClasses Tab = "classes"
)

// Valid reports whether t is a tab the list knows.
func (t Tab) Valid() bool { return t == TabAll || t == TabDirect || t == TabClasses }

// Channels reports whether the tab lists class channels.
func (t Tab) Channels() bool { return t == TabAll || t == TabClasses }

// Directs reports whether the tab lists direct threads, and with a search
// the students one could start.
func (t Tab) Directs() bool { return t == TabAll || t == TabDirect }

// Side is which side of the school a message's sender is on.
type Side string

const (
	SideStaff   Side = "staff"
	SideStudent Side = "student"
)

// The bounds of the staff list: the students a search offers to write to,
// and the characters of a row's preview.
const (
	MaxCandidates = 20
	PreviewLength = 120
)

var (
	// ErrNotFound is a thread the caller cannot read: a class out of their
	// scope, an account that is no messaging peer of theirs, or a missing id,
	// answered alike so ids stay unguessable.
	ErrNotFound = errors.New("messaging: not found")
	// ErrNotAllowed is a post the pair no longer qualifies for: a direct
	// thread's student is disabled or no longer a student, or a colleague is
	// disabled or no longer opens the teacher workspace. The thread stays
	// readable (MESSAGING_NOT_ALLOWED).
	ErrNotAllowed = errors.New("messaging: this pair may not message")
	// ErrClassArchived is an announcement to an archived class, whose channel
	// stays readable and takes no post (CLASS_ARCHIVED).
	ErrClassArchived = errors.New("messaging: the class is archived")
	// ErrForbidden is an announcement by a caller who may not write to classes
	// (teaching.classes.write); the permission gate answers it first.
	ErrForbidden = errors.New("messaging: posting needs teaching.classes.write")
)

// ThreadRef names a thread as the staff side addresses it: a class channel by
// its class, a direct thread by its student, a colleague thread by the
// colleague.
type ThreadRef struct {
	Kind        Kind
	ClassID     string
	StudentID   string
	ColleagueID string
}

// Class names one class, for the classes a thread's student is in.
type Class struct {
	ID   string
	Name string
}

// Preview is the newest message of a thread as a row of the list shows it.
type Preview struct {
	Text           *string
	AttachmentName *string
	Mine           bool
}

// PreviewOf words the newest message: its body on one line, cut to
// PreviewLength characters, and the name of its file.
func PreviewOf(body, attachmentName *string, mine bool) *Preview {
	p := &Preview{AttachmentName: attachmentName, Mine: mine}
	if body != nil {
		text := oneLine(*body, PreviewLength)
		p.Text = &text
	}
	return p
}

func oneLine(s string, limit int) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= limit {
		return s
	}
	return string([]rune(s)[:limit])
}

// StaffThread is a row of the staff Messages list. A class row's Title is its
// class's name and StudentCount its active students. A direct row's Title is
// its student's name, ClassNames the caller's classes the student is in now
// and OtherClassNames the names of the student's other current classes, those
// not archived that the caller does not teach; Former when the student was in
// one of the caller's classes and is in none now. A Candidate is a student a
// search offers to write to, with no thread yet.
type StaffThread struct {
	Ref             ThreadRef
	Title           string
	AvatarKey       *string
	AvatarURL       *string
	ClassNames      []string
	OtherClassNames []string
	StudentCount    *int
	Former          bool
	Candidate       bool
	Disabled        bool
	LastMessageAt   *time.Time
	Preview         *Preview
	Unread          int
}

// ThreadFilter is what the staff list reads: the caller, whose scope.all
// widens the class channels only, the tab, and a search on the students' and
// the classes' names.
type ThreadFilter struct {
	ActorID string
	All     bool
	Tab     Tab
	Query   string
}

// Peer is the student of a staff member's direct thread, as its header and
// Details show them: the caller's classes the student is in now, the names
// of the student's other current classes (never their ids), whether the
// student was in one of the caller's classes and is in none now, and whether
// a post would be taken.
type Peer struct {
	ID              string
	Name            string
	AvatarKey       *string
	AvatarURL       *string
	Classes         []Class
	OtherClassNames []string
	Former          bool
	Disabled        bool
	CanPost         bool
}

// Channel is a class's announcement channel as its header shows it: its
// class, whether it is archived, and its active students.
type Channel struct {
	ClassID      string
	Name         string
	Archived     bool
	StudentCount int
}
