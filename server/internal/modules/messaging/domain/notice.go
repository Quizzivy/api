package domain

import "time"

// ExcerptLength is how many characters of the newest message an in-app item
// quotes.
const ExcerptLength = 80

// Delivery is what a new message tells its thread's readers: the
// conversation, who wrote the message and what, and the readers it is for.
// They are the other participant of a direct thread, or the class's active
// students for an announcement, each enabled, never the sender, and none
// whose read pointer has already reached the message.
type Delivery struct {
	ConversationID string
	Kind           Kind
	ClassID        string
	ClassName      string
	StaffID        string
	StudentID      string
	SenderName     string
	Body           *string
	Filename       *string
	Readers        []string
}

// Notice is the in-app item one reader holds for one thread, written again
// for each new message while the thread stays unread (message.received for
// a staff reader, message.from_teacher for a student). ClassName is set for
// an announcement. A staff reader's item leads to StudentID's thread, a
// student's to TeacherID's thread or ClassID's channel.
type Notice struct {
	ReaderID       string
	ConversationID string
	ToStudent      bool
	SenderName     string
	ClassName      *string
	Excerpt        string
	StudentID      string
	TeacherID      string
	ClassID        string
}

// Notices words the delivery as one Notice per reader, quoting the message's
// body on one line, or its file's name when it has no body, cut to
// ExcerptLength characters.
func (d Delivery) Notices() []Notice {
	excerpt := ""
	switch {
	case d.Body != nil && *d.Body != "":
		excerpt = oneLine(*d.Body, ExcerptLength)
	case d.Filename != nil:
		excerpt = oneLine(*d.Filename, ExcerptLength)
	}
	out := make([]Notice, 0, len(d.Readers))
	for _, reader := range d.Readers {
		n := Notice{ReaderID: reader, ConversationID: d.ConversationID, SenderName: d.SenderName, Excerpt: excerpt}
		switch {
		case d.Kind == KindClass:
			n.ToStudent, n.ClassID, n.ClassName = true, d.ClassID, &d.ClassName
		case reader == d.StudentID:
			n.ToStudent, n.TeacherID = true, d.StaffID
		default:
			n.StudentID = d.StudentID
		}
		out = append(out, n)
	}
	return out
}

// ReadState is a reader's thread right after their pointer moved: its
// conversation, whether nothing past the pointer is left for them but their
// own messages, and the moment that was judged at, before any message it did
// not count was committed.
type ReadState struct {
	ConversationID string
	CaughtUp       bool
	At             time.Time
}
