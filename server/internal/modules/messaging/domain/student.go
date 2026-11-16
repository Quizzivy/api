package domain

import "time"

// StudentFacts is what the database holds about a student and the account a
// direct thread names as its teacher.
type StudentFacts struct {
	// Exists is false for an id no account has.
	Exists bool
	// Self is the student naming themselves.
	Self bool
	// Conversation is a direct thread between the two that exists, whoever
	// wrote first.
	Conversation bool
	// Teaches is the account being the teacher of a class the student is a
	// member of now (classes.teacher_id). A class the student left counts no
	// more: the student may not write first to a teacher they no longer have.
	Teaches bool
	// TeacherEnabled is the teacher's account not being disabled.
	TeacherEnabled bool
	// TeacherStaff is the teacher's account not passing R2's strict student
	// predicate.
	TeacherStaff bool
	// Student is the caller passing R2's strict student predicate
	// (app.student_like_roles), as the student of a direct thread must.
	Student bool
}

// Teacher reports whether the account is someone the student may name in a
// direct thread: one they already have a thread with, or, for a student, the
// teacher of a class they are in now. A thread outlives the class it started
// from, but a student cannot write first to a teacher of a class they left
// (the owner, 2026-11-13). scope.all plays no part: the caller is a student.
func (Eligibility) Teacher(f StudentFacts) bool {
	return f.Exists && !f.Self && (f.Conversation || (f.Teaches && f.Student))
}

// ReadAsStudent lets the student read their thread with a teacher the rules'
// Teacher admits: ErrNotFound for anyone else.
func (e Eligibility) ReadAsStudent(f StudentFacts) error {
	if !e.Teacher(f) {
		return ErrNotFound
	}
	return nil
}

// PostAsStudent lets the student post in a thread they may read while the
// teacher's account is enabled and staff and the caller's is a student:
// ErrNotAllowed otherwise, the thread staying readable.
func (e Eligibility) PostAsStudent(f StudentFacts) error {
	if err := e.ReadAsStudent(f); err != nil {
		return err
	}
	if !f.TeacherEnabled || !f.TeacherStaff || !f.Student {
		return ErrNotAllowed
	}
	return nil
}

// CanPostAsStudent reports whether PostAsStudent would let the post through.
func (e Eligibility) CanPostAsStudent(f StudentFacts) bool { return e.PostAsStudent(f) == nil }

// StudentThread is a row of the student's Messages. A direct row names its
// teacher by TeacherID, its Title the teacher's name and ClassNames the
// student's classes that teacher teaches now ("Your teacher · …"); Former
// when the teacher taught the student and teaches them in no class now
// ("Former teacher"). A class row names its class by ClassID and is titled
// with the class's name ("Class announcements").
type StudentThread struct {
	Kind          Kind
	ClassID       string
	TeacherID     string
	Title         string
	AvatarKey     *string
	AvatarURL     *string
	ClassNames    []string
	Former        bool
	LastMessageAt *time.Time
	Preview       *Preview
	Unread        int
}

// Teacher is the teacher of a student's direct thread, as its header shows
// them: the student's classes the teacher teaches now, whether the teacher
// taught the student and teaches them in none now, and whether a post would
// be taken.
type Teacher struct {
	ID         string
	Name       string
	AvatarKey  *string
	AvatarURL  *string
	ClassNames []string
	Former     bool
	CanPost    bool
}

// Sender names the author of a post, with the stored key of their photo and
// its signed URL.
type Sender struct {
	ID        string
	Name      string
	AvatarKey *string
	AvatarURL *string
}

// StudentChannel is a class's channel as its student sees it: the class and
// its teacher, whose direct thread is where a reply goes.
type StudentChannel struct {
	ClassID string
	Name    string
	Teacher Sender
}

// StudentMessage is a message as a student reads it: its own type, never a
// Message handed on (D17, DG-91). It has no side, no "Seen by", no member
// count and no attempt events.
type StudentMessage struct {
	ID         string
	Sender     Sender
	Body       *string
	Attachment *Attachment
	CreatedAt  time.Time
	Mine       bool
}

// ForStudent is m as a student reads it.
func (m Message) ForStudent() StudentMessage {
	return StudentMessage{
		ID:         m.ID,
		Sender:     Sender{ID: m.SenderID, Name: m.SenderName, AvatarKey: m.SenderAvatarKey, AvatarURL: m.SenderAvatarURL},
		Body:       m.Body,
		Attachment: m.Attachment,
		CreatedAt:  m.CreatedAt,
		Mine:       m.Mine,
	}
}

// StudentPage is one page of a thread as a student reads it, oldest first,
// with Page's cursors.
type StudentPage struct {
	Messages   []StudentMessage
	NextBefore *string
	LatestID   *string
}

// ForStudent is p as a student reads it.
func (p Page) ForStudent() StudentPage {
	out := StudentPage{Messages: make([]StudentMessage, len(p.Messages)), NextBefore: p.NextBefore, LatestID: p.LatestID}
	for i, m := range p.Messages {
		out.Messages[i] = m.ForStudent()
	}
	return out
}

// StudentPosted is a student's post's outcome: the message, and whether this
// call made it or its clientId repeated one already made.
type StudentPosted struct {
	Message StudentMessage
	Created bool
}

// ContextEvent is a flagged attempt of a direct thread's student on an
// assignment its staff reader can read, shown in the stream at At. It is
// computed when the thread is read, never stored as a message, and never
// unread.
type ContextEvent struct {
	AttemptID    string
	AssignmentID string
	Title        string
	FocusLost    int
	At           time.Time
}

// ContextWindow is how long before a thread's first message its context
// events start.
const ContextWindow = 24 * time.Hour
