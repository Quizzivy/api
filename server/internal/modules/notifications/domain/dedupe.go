package domain

import (
	"strconv"
	"strings"
	"time"
)

const (
	// SubmittedBucket is how long papers handed in on one assignment merge
	// into one notification for a teacher.
	SubmittedBucket = 15 * time.Minute
	// DueLookback is how far back a due-time item is still written: one whose
	// moment is older than this was missed, and is not made up.
	DueLookback = 7 * 24 * time.Hour
	// DueEvery is the least time between two materialisations for one user.
	DueEvery = 5 * time.Minute
)

// Lead names how long before a close a due-soon notification falls.
type Lead string

const (
	// LeadDay is a day before the close.
	LeadDay Lead = "24h"
	// LeadHour is an hour before the close.
	LeadHour Lead = "1h"
)

// SubmittedKey is the dedupe key of the papers handed in on an assignment
// during the fifteen minutes at falls in.
func SubmittedKey(assignmentID string, at time.Time) string {
	return "submitted:" + assignmentID + ":" + strconv.FormatInt(at.Unix()/int64(SubmittedBucket/time.Second), 10)
}

// FlaggedKey is the dedupe key of the flag on one attempt.
func FlaggedKey(attemptID string) string {
	return "flagged:" + attemptID
}

// FlaggedBucket is how often, at most, a teacher is told again of the
// absences on one flagged attempt of a flexible assignment (F1).
const FlaggedBucket = 15 * time.Minute

// FlaggedAgainKey is the dedupe key of the absences counted on one flagged
// attempt during the FlaggedBucket at falls in: FlaggedKey and the bucket.
func FlaggedAgainKey(attemptID string, at time.Time) string {
	return FlaggedKey(attemptID) + ":" + strconv.FormatInt(at.Unix()/int64(FlaggedBucket/time.Second), 10)
}

// FlaggedAgainBucket is the bucket a FlaggedAgainKey names, and false for any
// other key, FlaggedKey's among them.
func FlaggedAgainBucket(key string) (string, bool) {
	rest, ok := strings.CutPrefix(key, "flagged:")
	if !ok {
		return "", false
	}
	_, bucket, ok := strings.Cut(rest, ":")
	return bucket, ok && bucket != ""
}

// JoinedKey is the dedupe key of one student joining one class.
func JoinedKey(classID, studentID string) string {
	return "joined:" + classID + ":" + studentID
}

// ExtendedKey is the dedupe key of an assignment's close being moved for one
// student: a later move replaces the earlier one.
func ExtendedKey(assignmentID string) string {
	return "extended:" + assignmentID
}

// OpenedKey is the dedupe key of an assignment opening for one student.
func OpenedKey(assignmentID string) string {
	return "opened:" + assignmentID
}

// DueSoonKey is the dedupe key of the reminder lead before a student's close.
// It names the close, so a close that moves earns its reminders again.
func DueSoonKey(assignmentID string, closes time.Time, lead Lead) string {
	return "due_soon:" + assignmentID + ":" + strconv.FormatInt(closes.Unix(), 10) + ":" + string(lead)
}

// ClosingKey is the dedupe key of the warning that an assignment closes
// within the hour. It names the close, as DueSoonKey does.
func ClosingKey(assignmentID string, closes time.Time) string {
	return "closing:" + assignmentID + ":" + strconv.FormatInt(closes.Unix(), 10)
}

// ReadyKey is the dedupe key of one attempt's result being ready.
func ReadyKey(attemptID string) string {
	return "ready:" + attemptID
}

// AllowanceWarningKey is the dedupe key of a teacher's warning that the month's
// allowance is nearly used. The month is its first day, as YYYY-MM-DD.
func AllowanceWarningKey(month string) string {
	return "ai_warning:" + month
}

// CreditRequestKey is the dedupe key of an Admin being told of one request.
func CreditRequestKey(requestID string) string {
	return "ai_request:" + requestID
}

// CreditDecisionKey is the dedupe key of a teacher being told how one request
// was decided.
func CreditDecisionKey(requestID string) string {
	return "ai_decision:" + requestID
}

// TransferredKey is the dedupe key of a teacher being told that one disabled
// teacher's content was handed to them: a second hand-over replaces the first.
func TransferredKey(fromUserID string) string {
	return "transferred:" + fromUserID
}

// TeacherChangedKey is the dedupe key of a teacher being told that a class's
// teacher changed: a later change replaces the item.
func TeacherChangedKey(classID string) string {
	return "teacher_changed:" + classID
}

// SharedKey is the dedupe key of a teacher being told that a test or a
// question set was shared with them: a later share of the same content, after
// a revocation, replaces the item.
func SharedKey(kind, resourceID string) string {
	return "shared:" + kind + ":" + resourceID
}

// CollaboratorPublishedKey is the dedupe key of an owner being told that a
// co-editor published version of their test: each version is told once.
func CollaboratorPublishedKey(testID string, version int) string {
	return "collaborator_published:" + testID + ":" + strconv.Itoa(version)
}

// MessageKeyPrefix begins the dedupe key of every message item, so that a
// statement can name the items of a set of threads.
const MessageKeyPrefix = "msg:"

// MessageKey is the dedupe key of the item a reader holds for one thread,
// the conversation conversationID: one per reader per thread.
func MessageKey(conversationID string) string {
	return MessageKeyPrefix + conversationID
}

// ConversationOf is the conversation a MessageKey names, and false for a key
// that is not a message item's.
func ConversationOf(key string) (string, bool) {
	id, ok := strings.CutPrefix(key, MessageKeyPrefix)
	return id, ok && id != ""
}

// SharedOf is the kind and the id of the content a SharedKey names, and false
// for a key that is not a share's.
func SharedOf(key string) (kind, resourceID string, ok bool) {
	rest, ok := strings.CutPrefix(key, "shared:")
	if !ok {
		return "", "", false
	}
	kind, resourceID, ok = strings.Cut(rest, ":")
	return kind, resourceID, ok && kind != "" && resourceID != ""
}

// AllowanceWarningOf is the month an AllowanceWarningKey names, its first day
// as 00:00 UTC, and false for a key that is not an allowance warning's.
func AllowanceWarningOf(key string) (time.Time, bool) {
	month, ok := strings.CutPrefix(key, "ai_warning:")
	if !ok {
		return time.Time{}, false
	}
	at, err := time.Parse(time.DateOnly, month)
	return at, err == nil && at.Day() == 1
}

// CreditRequestOf is the request a CreditRequestKey names, and false for a
// key that is not one.
func CreditRequestOf(key string) (string, bool) {
	id, ok := strings.CutPrefix(key, "ai_request:")
	return id, ok && id != ""
}

// CreditDecisionOf is the request a CreditDecisionKey names, and false for a
// key that is not one.
func CreditDecisionOf(key string) (string, bool) {
	id, ok := strings.CutPrefix(key, "ai_decision:")
	return id, ok && id != ""
}

// TeacherChangedOf is the class a TeacherChangedKey names, and false for a
// key that is not a change of teacher's.
func TeacherChangedOf(key string) (string, bool) {
	id, ok := strings.CutPrefix(key, "teacher_changed:")
	return id, ok && id != ""
}

// SubmittedFrom is the start of the bucket a SubmittedKey names, and false
// for a key that is not one.
func SubmittedFrom(key string) (time.Time, bool) {
	rest, ok := strings.CutPrefix(key, "submitted:")
	i := strings.LastIndexByte(rest, ':')
	if !ok || i <= 0 {
		return time.Time{}, false
	}
	bucket, err := strconv.ParseInt(rest[i+1:], 10, 64)
	if err != nil || bucket < 0 {
		return time.Time{}, false
	}
	return time.Unix(bucket*int64(SubmittedBucket/time.Second), 0).UTC(), true
}

// SubmittedBucketOf is the start of the fifteen-minute bucket SubmittedKey
// puts at in.
func SubmittedBucketOf(at time.Time) time.Time {
	size := int64(SubmittedBucket / time.Second)
	return time.Unix(at.Unix()/size*size, 0).UTC()
}
