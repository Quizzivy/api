package domain

import (
	"context"
	"time"

	"quizzivy/internal/shared/access"
)

// Repository keeps conversations, messages and read pointers in
// app.conversations, app.messages and app.conversation_reads, and reads the
// classes, accounts and ties (app.messaging_ties) that decide who reads and
// who posts. It writes no audit row (D17).
type Repository interface {
	// DirectFacts reads what decides staffID's direct thread with studentID.
	DirectFacts(ctx context.Context, staffID, studentID string) (DirectFacts, error)
	// Peer reads studentID as the header of staffID's thread with them shows
	// the student. The caller has checked ReadDirect; CanPost is left false.
	Peer(ctx context.Context, staffID, studentID string) (Peer, error)
	// Channel reads the class classID as scope reaches it, archived or not, or
	// ErrNotFound.
	Channel(ctx context.Context, scope access.Scope, classID string) (Channel, error)
	// Threads returns the staff list's rows for f: class channels and direct
	// threads, newest activity first, a channel nobody has posted in last by
	// name, then, with a query, at most MaxCandidates students to write to.
	Threads(ctx context.Context, f ThreadFilter) ([]StaffThread, error)
	// DirectPage reads one page of staffID's thread with studentID, empty when
	// none exists yet.
	DirectPage(ctx context.Context, staffID, studentID string, cursor Cursor) (Page, error)
	// ChannelPage reads one page of the channel of classID, each post with its
	// SeenBy.
	ChannelPage(ctx context.Context, classID string, cursor Cursor) (Page, error)
	// PostDirect writes post to staffID's thread with studentID in one
	// transaction, judging the facts it reads there by Eligibility: a thread
	// the sender cannot read answers ErrNotFound, a repeated clientId returns
	// the message it made (ErrClientIDTaken when that is another thread's),
	// a pair that no longer qualifies answers ErrNotAllowed, the first post
	// makes the thread, and every post moves its last_message_at.
	PostDirect(ctx context.Context, staffID, studentID string, post Post) (Posted, error)
	// PostChannel is PostDirect for the channel of classID, as scope reaches
	// the class, which it holds FOR SHARE: mayWrite is whether the sender
	// holds teaching.classes.write, and an archived class answers
	// ErrClassArchived.
	PostChannel(ctx context.Context, scope access.Scope, classID string, mayWrite bool, post Post) (Posted, error)
	// MarkDirectRead moves the reader's pointer in staffID's thread with
	// studentID forward to the mark's message, or answers ErrNotInThread.
	MarkDirectRead(ctx context.Context, staffID, studentID string, mark ReadMark) error
	// MarkChannelRead is MarkDirectRead for the channel of classID.
	MarkChannelRead(ctx context.Context, classID string, mark ReadMark) error
	// FirstMessageAt is when the first message of staffID's thread with
	// studentID was written, nil while the thread has none.
	FirstMessageAt(ctx context.Context, staffID, studentID string) (*time.Time, error)

	// StudentFacts reads what decides studentID's direct thread with
	// teacherID.
	StudentFacts(ctx context.Context, studentID, teacherID string) (StudentFacts, error)
	// Teacher reads teacherID as the header of studentID's thread with them
	// shows the teacher. The caller has checked ReadAsStudent; CanPost is
	// left false.
	Teacher(ctx context.Context, studentID, teacherID string) (Teacher, error)
	// StudentChannel reads the class classID for its member studentID,
	// archived or not, or ErrNotFound.
	StudentChannel(ctx context.Context, studentID, classID string) (StudentChannel, error)
	// StudentThreads returns studentID's Messages: their direct threads and
	// the channels of the classes they are in that are not archived and have
	// a post, newest activity first.
	StudentThreads(ctx context.Context, studentID string) ([]StudentThread, error)
	// StudentChannelPage reads one page of the channel of classID with no
	// SeenBy.
	StudentChannelPage(ctx context.Context, classID string, cursor Cursor) (Page, error)
	// PostAsStudent writes post to studentID's thread with teacherID in one
	// transaction, as PostDirect does, judging the facts it reads there by
	// Eligibility's student rules. A first post holds the student's
	// memberships of the teacher's classes FOR SHARE, so a removal waits for
	// it.
	PostAsStudent(ctx context.Context, studentID, teacherID string, post Post) (Posted, error)
}
