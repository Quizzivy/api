package domain

import (
	"strconv"
	"time"
)

// EmailTemplate names a notification email as R5's outbox stores it (its
// template column). Each is one of platform/email's templates of the same
// name; a test holds the two lists to each other.
type EmailTemplate string

// The notification emails (T-R7.13).
const (
	EmailAttemptFlagged      EmailTemplate = "attempt_flagged"
	EmailAssignmentExtended  EmailTemplate = "assignment_extended"
	EmailAssignmentDueSoon   EmailTemplate = "assignment_due_soon"
	EmailAssignmentClosing   EmailTemplate = "assignment_closing"
	EmailResultReady         EmailTemplate = "result_ready"
	EmailContentShared       EmailTemplate = "content_shared"
	EmailClassTeacherChanged EmailTemplate = "class_teacher_changed"
	EmailSubmissionsDigest   EmailTemplate = "submissions_digest"
	EmailMessageReceived     EmailTemplate = "message_received"
	EmailWeeklySummary       EmailTemplate = "weekly_summary"
)

// The AI assistant's notification emails (T-R7.23). No switch governs them.
const (
	EmailAIAllowanceWarning     EmailTemplate = "ai_allowance_warning"
	EmailAICreditRequest        EmailTemplate = "ai_credit_request"
	EmailAICreditRequestDecided EmailTemplate = "ai_credit_request_decided"
)

var killSwitch = map[EmailTemplate]string{
	EmailAttemptFlagged: "attempt.flagged", EmailAssignmentExtended: "assignment.extended",
	EmailAssignmentDueSoon: "assignment.due_soon", EmailAssignmentClosing: "assignment.closing",
	EmailResultReady: "result.ready", EmailContentShared: "content.shared",
	EmailClassTeacherChanged: "class.teacher_changed", EmailSubmissionsDigest: "attempt.submitted",
	EmailMessageReceived: "message.received", EmailWeeklySummary: "summary.weekly",
	EmailAIAllowanceWarning: "ai.allowance_warning", EmailAICreditRequest: "ai.credit_request",
	EmailAICreditRequestDecided: "ai.credit_request.decided",
}

// Event is the NOTIFY_EMAIL_EVENTS name that turns t off for everyone.
func (t EmailTemplate) Event() string { return killSwitch[t] }

const (
	// MessageEmailDelay is how long a message's email waits, so that a reader
	// who opens the thread in the meantime gets none.
	MessageEmailDelay = 10 * time.Minute
	// MessageEmailSpacing is the least time between two emails about one
	// thread to one reader.
	MessageEmailSpacing = 30 * time.Minute
)

// EmailIDs are what a notification email's outbox row holds: ids, and the
// four facts that are not names (the bucket's start, whether a teacher is the
// one a class left, a reminder's lead, an allowance warning's month). Everything the email says is read when
// it is sent. A digest's AttemptID is the paper its notice announced; the
// store adds each later paper of the bucket to the row.
type EmailIDs struct {
	AttemptID      string
	AssignmentID   string
	ConversationID string
	StudentID      string
	TeacherID      string
	ClassID        string
	ContentKind    string
	ResourceID     string
	RequestID      string
	From           time.Time
	Month          time.Time
	Previous       bool
	Lead           Lead
}

// Email is one notification email to enqueue for UserID: its template and
// ids, the switch whose email column governs it (nil for an email no switch
// governs: a share, a change of teacher, the AI assistant's), its dedupe key,
// and when it falls due (zero is now). Thread is a message email's conversation: it is sent at
// most once per MessageEmailSpacing per thread and reader, so the store adds
// to DedupeKey the last such email it closed and holds DeliverAfter until
// MessageEmailSpacing after the last one sent. Summary is a weekly
// summary's figures, which the scheduler computes once and the row holds
// (T-R7.14).
type Email struct {
	UserID       string
	Template     EmailTemplate
	IDs          EmailIDs
	Switch       *Event
	DedupeKey    string
	DeliverAfter time.Time
	Thread       string
	Summary      *WeeklyFigures
}

// PlanEmail is the email n calls for when Notify writes it at now, and false
// for a notice that sends none: the flag on a paper, a moved close, a result
// shown when it is graded, a share, a change of teacher, the AI assistant's
// warning, request and decision (T-R7.23), the papers of a
// fifteen-minute bucket, due at its end, and a message, due
// MessageEmailDelay later. A paper falls in the bucket its in-app item does
// (the notice's SubmittedKey, the moment it was announced), not the one its
// submitted_at names: a paper whose time ran out is closed, and announced,
// when someone next touches it, with submitted_at its deadline. The digest
// counts the papers its bucket's notices named (EmailIDs.AttemptID), so the
// bell and the email agree. The due-time items written when a user reads their
// notifications never come through Notify and send nothing here.
func PlanEmail(n Notice, now time.Time) (Email, bool) {
	e := Email{UserID: n.UserID}
	if event, ok := n.Kind.Event(); ok {
		e.Switch = &event
	}
	var t Target
	if n.Target != nil {
		t = *n.Target
	}
	switch n.Kind {
	case AttemptFlagged:
		return e.of(EmailAttemptFlagged, EmailIDs{AttemptID: t.AttemptID}, t.AttemptID, "flagged:"+t.AttemptID+":"+n.UserID)
	case AssignmentExtended:
		extended, _ := n.Params.(Extended)
		return e.of(EmailAssignmentExtended, EmailIDs{AssignmentID: t.AssignmentID}, t.AssignmentID,
			"extended:"+t.AssignmentID+":"+n.UserID+":"+unix(extended.ClosesAt))
	case ResultReady:
		return e.of(EmailResultReady, EmailIDs{AttemptID: t.AttemptID}, t.AttemptID, "ready:"+t.AttemptID)
	case AttemptSubmitted:
		from, ok := SubmittedFrom(n.DedupeKey)
		if !ok {
			from = SubmittedBucketOf(now)
		}
		if n.AttemptID == "" {
			return Email{}, false
		}
		e.DeliverAfter = from.Add(SubmittedBucket)
		return e.of(EmailSubmissionsDigest, EmailIDs{AssignmentID: t.AssignmentID, AttemptID: n.AttemptID, From: from}, t.AssignmentID,
			"submitted:"+t.AssignmentID+":"+n.UserID+":"+unix(from))
	case MessageReceived, MessageFromTeacher:
		id, _ := ConversationOf(n.DedupeKey)
		e.DeliverAfter, e.Thread = now.Add(MessageEmailDelay), id
		return e.of(EmailMessageReceived, EmailIDs{ConversationID: id, StudentID: t.StudentID, TeacherID: t.TeacherID, ClassID: t.ClassID},
			id, "message:"+id+":"+n.UserID)
	}
	return switchlessEmail(e, n, now)
}

func switchlessEmail(e Email, n Notice, now time.Time) (Email, bool) {
	minute := strconv.FormatInt(now.Unix()/60, 10)
	switch n.Kind {
	case ContentShared:
		kind, id, _ := SharedOf(n.DedupeKey)
		return e.of(EmailContentShared, EmailIDs{ContentKind: kind, ResourceID: id}, id,
			"shared:"+kind+":"+id+":"+n.UserID+":"+minute)
	case ClassTeacherChanged:
		id, _ := TeacherChangedOf(n.DedupeKey)
		changed, _ := n.Params.(TeacherChanged)
		return e.of(EmailClassTeacherChanged, EmailIDs{ClassID: id, Previous: changed.TeacherName != nil}, id,
			"teacher_changed:"+id+":"+n.UserID+":"+minute)
	}
	return aiEmail(e, n)
}

// aiEmail is the email of the AI assistant's three notices, which no switch
// governs (T-R7.23): the allowance warning, once a month per teacher as its
// item is; a request, to each holder of ai.credits.manage its item goes to;
// a decision, to the teacher who asked.
func aiEmail(e Email, n Notice) (Email, bool) {
	switch n.Kind {
	case AIAllowanceWarning:
		month, ok := AllowanceWarningOf(n.DedupeKey)
		if !ok {
			return Email{}, false
		}
		day := month.Format(time.DateOnly)
		return e.of(EmailAIAllowanceWarning, EmailIDs{Month: month}, day, "ai_warning:"+n.UserID+":"+day)
	case AICreditRequest:
		if id, ok := CreditRequestOf(n.DedupeKey); ok {
			return e.of(EmailAICreditRequest, EmailIDs{RequestID: id}, id, "ai_request:"+id+":"+n.UserID)
		}
	case AICreditRequestDecided:
		if id, ok := CreditDecisionOf(n.DedupeKey); ok {
			return e.of(EmailAICreditRequestDecided, EmailIDs{RequestID: id}, id, "ai_decision:"+id+":"+n.UserID)
		}
	}
	return Email{}, false
}

// of completes e as template's email about the thing whose id is about,
// keyed by key, and refuses one about nothing.
func (e Email) of(template EmailTemplate, ids EmailIDs, about, key string) (Email, bool) {
	if about == "" {
		return Email{}, false
	}
	e.Template, e.IDs, e.DedupeKey = template, ids, "email:"+key
	return e, true
}

func unix(at time.Time) string { return strconv.FormatInt(at.Unix(), 10) }
