package email

import (
	"net/url"
	"time"
)

// The templates of the notification emails (T-R7.13). Each is written for one
// event of the recipient's notification switches, or for one that has none,
// and each is Prepared: its outbox row holds ids, and the template's Preparer
// reads what the message says, and whether it is still worth sending, when
// the dispatcher is about to send it. No params type here carries a message
// body, a join code, a password or a score.
const (
	AttemptFlagged      Template = "attempt_flagged"
	AssignmentExtended  Template = "assignment_extended"
	AssignmentDueSoon   Template = "assignment_due_soon"
	AssignmentClosing   Template = "assignment_closing"
	ResultReady         Template = "result_ready"
	ContentShared       Template = "content_shared"
	ClassTeacherChanged Template = "class_teacher_changed"
	SubmissionsDigest   Template = "submissions_digest"
	MessageReceived     Template = "message_received"
	WeeklySummary       Template = "weekly_summary"
)

// The values of ContentSharedParams.Kind and .Access, as the contract's
// ShareKind and ShareAccess.
const (
	SharedTest        = "test"
	SharedQuestionSet = "question_set"
	SharedUse         = "use"
	SharedEdit        = "edit"
)

// The leads of AssignmentDueSoonParams, as the notifications module's.
const (
	LeadDay  = "24h"
	LeadHour = "1h"
)

// AttemptFlaggedParams tell a teacher that the integrity policy flagged a
// student's attempt. The outbox holds AttemptID; FullName (the recipient's),
// StudentName, Title and FocusLost are read when it is sent.
type AttemptFlaggedParams struct {
	AttemptID   string `json:"attemptId"`
	FullName    string `json:"fullName,omitempty"`
	StudentName string `json:"studentName,omitempty"`
	Title       string `json:"title,omitempty"`
	FocusLost   int    `json:"focusLost,omitempty"`
}

// AssignmentExtendedParams tell a student that a teacher moved their close.
// The outbox holds AssignmentID; FullName, Title and ClosesAt, the student's
// own close, are read when it is sent.
type AssignmentExtendedParams struct {
	AssignmentID string    `json:"assignmentId"`
	FullName     string    `json:"fullName,omitempty"`
	Title        string    `json:"title,omitempty"`
	ClosesAt     time.Time `json:"closesAt,omitzero"`
}

// AssignmentDueSoonParams remind a student, LeadDay or LeadHour before their
// close, of an assignment they have not handed in. The outbox holds
// AssignmentID and Lead; FullName, Title and ClosesAt are read when it is
// sent.
type AssignmentDueSoonParams struct {
	AssignmentID string    `json:"assignmentId"`
	Lead         string    `json:"lead"`
	FullName     string    `json:"fullName,omitempty"`
	Title        string    `json:"title,omitempty"`
	ClosesAt     time.Time `json:"closesAt,omitzero"`
}

// AssignmentClosingParams warn a teacher that an assignment closes within the
// hour with students still to hand in. The outbox holds AssignmentID;
// FullName, Title, ClosesAt and NotSubmitted are read when it is sent.
type AssignmentClosingParams struct {
	AssignmentID string    `json:"assignmentId"`
	FullName     string    `json:"fullName,omitempty"`
	Title        string    `json:"title,omitempty"`
	ClosesAt     time.Time `json:"closesAt,omitzero"`
	NotSubmitted int       `json:"notSubmitted,omitempty"`
}

// ResultReadyParams tell a student that their result can be seen. The outbox
// holds AttemptID; FullName and Title are read when it is sent. It names no
// score: the result page shows it under the review policy in force.
type ResultReadyParams struct {
	AttemptID string `json:"attemptId"`
	FullName  string `json:"fullName,omitempty"`
	Title     string `json:"title,omitempty"`
}

// ContentSharedParams tell a teacher that a colleague shared a test or a
// question set with them. The outbox holds Kind and ResourceID; FullName,
// SharerName, Name, Access and the sharer's optional Message are read from
// the grant when it is sent.
type ContentSharedParams struct {
	Kind       string  `json:"kind"`
	ResourceID string  `json:"resourceId"`
	FullName   string  `json:"fullName,omitempty"`
	SharerName string  `json:"sharerName,omitempty"`
	Name       string  `json:"name,omitempty"`
	Access     string  `json:"access,omitempty"`
	Message    *string `json:"message,omitempty"`
}

// ClassTeacherChangedParams tell a teacher that an Admin gave a class to
// another teacher: the new teacher, or with Previous the one who taught it
// before. The outbox holds ClassID and Previous; FullName, ClassName and, for
// the previous teacher, TeacherName, who teaches it now, are read when it is
// sent.
type ClassTeacherChangedParams struct {
	ClassID     string `json:"classId"`
	Previous    bool   `json:"previous,omitempty"`
	FullName    string `json:"fullName,omitempty"`
	ClassName   string `json:"className,omitempty"`
	TeacherName string `json:"teacherName,omitempty"`
}

// SubmissionsDigestParams tell a teacher how many papers were handed in on an
// assignment during one fifteen-minute bucket, sent at its end. The outbox
// holds AssignmentID and From, the bucket's start; FullName, Title, Count and
// ToGrade are read when it is sent.
type SubmissionsDigestParams struct {
	AssignmentID string    `json:"assignmentId"`
	From         time.Time `json:"from"`
	FullName     string    `json:"fullName,omitempty"`
	Title        string    `json:"title,omitempty"`
	Count        int       `json:"count,omitempty"`
	ToGrade      int       `json:"toGrade,omitempty"`
}

// MessageReceivedParams tell a reader that a thread holds messages they have
// not read. It never carries a message's body or a file's name: it names the
// sender and the class and links to the thread (D17). The outbox holds
// ConversationID and the thread's route: StudentID for a staff reader's
// direct thread, TeacherID for a student's, ClassID for an announcement.
// FullName, SenderName, ClassName and Count are read when it is sent.
type MessageReceivedParams struct {
	ConversationID string `json:"conversationId"`
	StudentID      string `json:"studentId,omitempty"`
	TeacherID      string `json:"teacherId,omitempty"`
	ClassID        string `json:"classId,omitempty"`
	FullName       string `json:"fullName,omitempty"`
	SenderName     string `json:"senderName,omitempty"`
	ClassName      string `json:"className,omitempty"`
	Count          int    `json:"count,omitempty"`
}

// WeeklySummaryParams are a teacher's Monday summary of the week that began
// on WeekOf: papers handed in, answers waiting for a mark and flagged
// attempts on the classes ClassNames names (at most five). T-R7.14's
// scheduler writes the figures; FullName is read when it is sent.
type WeeklySummaryParams struct {
	WeekOf     time.Time `json:"weekOf"`
	Count      int       `json:"count"`
	ToGrade    int       `json:"toGrade"`
	Flagged    int       `json:"flagged"`
	ClassNames []string  `json:"classNames"`
	FullName   string    `json:"fullName,omitempty"`
}

// DigestBucket is how long the papers one SubmissionsDigestParams counts
// were handed in over: the notifications module's fifteen-minute bucket.
const DigestBucket = 15 * time.Minute

// Until is the end of the digest's bucket, when it is sent.
func (p SubmissionsDigestParams) Until() time.Time { return p.From.Add(DigestBucket) }

func (AttemptFlaggedParams) Template() Template { return AttemptFlagged }

func (AssignmentExtendedParams) Template() Template { return AssignmentExtended }

func (AssignmentDueSoonParams) Template() Template { return AssignmentDueSoon }

func (AssignmentClosingParams) Template() Template { return AssignmentClosing }

func (ResultReadyParams) Template() Template { return ResultReady }

func (ContentSharedParams) Template() Template { return ContentShared }

func (ClassTeacherChangedParams) Template() Template { return ClassTeacherChanged }

func (SubmissionsDigestParams) Template() Template { return SubmissionsDigest }

func (MessageReceivedParams) Template() Template { return MessageReceived }

func (WeeklySummaryParams) Template() Template { return WeeklySummary }

// The paths of the app's pages the notification emails link to: the teacher
// workspace on the staff host and the student's pages on the student host.
const (
	staffSettingsPath   = "/teacher/settings/notifications"
	studentSettingsPath = "/settings/notifications"
)

func idPath(prefix string, id func(Params) string) func(string, Params, string) string {
	return func(appURL string, p Params, _ string) string {
		return appURL + prefix + url.PathEscape(id(p))
	}
}

func messageLink(appURL string, p Params, _ string) string {
	m := p.(MessageReceivedParams)
	switch {
	case m.StudentID != "":
		return appURL + "/teacher/messages/student/" + url.PathEscape(m.StudentID)
	case m.TeacherID != "":
		return appURL + "/messages/teacher/" + url.PathEscape(m.TeacherID)
	default:
		return appURL + "/messages/class/" + url.PathEscape(m.ClassID)
	}
}

func teacherChangedLink(appURL string, p Params, _ string) string {
	c := p.(ClassTeacherChangedParams)
	if c.Previous {
		return appURL + "/teacher/classes"
	}
	return appURL + "/teacher/classes/" + url.PathEscape(c.ClassID)
}

func fixedLink(path string) func(string, Params, string) string {
	return func(appURL string, _ Params, _ string) string { return appURL + path }
}

var noticeDefinitions = map[Template]definition{
	AttemptFlagged: {staff: true, prepared: true, event: "attempt.flagged", decode: decoder[AttemptFlaggedParams](),
		link: idPath("/teacher/attempts/", func(p Params) string { return p.(AttemptFlaggedParams).AttemptID })},
	AssignmentExtended: {prepared: true, event: "assignment.extended", decode: decoder[AssignmentExtendedParams](),
		link: idPath("/assignments/", func(p Params) string { return p.(AssignmentExtendedParams).AssignmentID })},
	AssignmentDueSoon: {prepared: true, event: "assignment.due_soon", decode: decoder[AssignmentDueSoonParams](),
		link: idPath("/assignments/", func(p Params) string { return p.(AssignmentDueSoonParams).AssignmentID })},
	AssignmentClosing: {staff: true, prepared: true, event: "assignment.closing", decode: decoder[AssignmentClosingParams](),
		link: idPath("/teacher/assignments/", func(p Params) string { return p.(AssignmentClosingParams).AssignmentID })},
	ResultReady: {prepared: true, event: "result.ready", decode: decoder[ResultReadyParams](),
		link: func(appURL string, p Params, _ string) string {
			return appURL + "/attempts/" + url.PathEscape(p.(ResultReadyParams).AttemptID) + "/result"
		}},
	ContentShared: {staff: true, prepared: true, event: "content.shared", decode: decoder[ContentSharedParams](),
		link: fixedLink("/teacher/shared-with-me")},
	ClassTeacherChanged: {staff: true, prepared: true, event: "class.teacher_changed", decode: decoder[ClassTeacherChangedParams](),
		link: teacherChangedLink},
	SubmissionsDigest: {staff: true, prepared: true, event: "attempt.submitted", decode: decoder[SubmissionsDigestParams](),
		link: idPath("/teacher/assignments/", func(p Params) string { return p.(SubmissionsDigestParams).AssignmentID })},
	MessageReceived: {prepared: true, event: "message.received", decode: decoder[MessageReceivedParams](), link: messageLink},
	WeeklySummary: {staff: true, prepared: true, event: "summary.weekly", decode: decoder[WeeklySummaryParams](),
		link: fixedLink("/teacher")},
}

// noticeTemplates lists the notification emails in the order Templates
// answers them.
var noticeTemplates = []Template{
	AttemptFlagged, AssignmentExtended, AssignmentDueSoon, AssignmentClosing, ResultReady,
	ContentShared, ClassTeacherChanged, SubmissionsDigest, MessageReceived, WeeklySummary,
}

// Prepared reports whether t's outbox rows hold ids alone, so that a
// dispatcher sends one only through t's Preparer: a dispatcher without one
// leaves the row pending for one that has it.
func Prepared(t Template) bool { return definitions[t].prepared }

// Event is the name NOTIFY_EMAIL_EVENTS turns t's email off by, and empty for
// a template no such name governs (R5's account and export mail).
func Event(t Template) string { return definitions[t].event }

// NotifyEvents lists the names NOTIFY_EMAIL_EVENTS takes, one per
// notification email, in Templates' order.
func NotifyEvents() []string {
	out := make([]string, len(noticeTemplates))
	for i, t := range noticeTemplates {
		out[i] = definitions[t].event
	}
	return out
}
