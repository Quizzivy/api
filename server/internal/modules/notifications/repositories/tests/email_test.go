//go:build integration

package repositories_test

import (
	"context"
	"testing"
	"time"

	"quizzivy/internal/core/adapters"
	notificationsemail "quizzivy/internal/modules/notifications/application/email"
	"quizzivy/internal/modules/notifications/domain"
	"quizzivy/internal/modules/notifications/repositories"
	"quizzivy/internal/platform/db"
	"quizzivy/internal/platform/email"
)

type queued struct {
	template, key, params string
	deliverAfter          time.Time
}

func (w *dueWorld) mailStore() *repositories.Mail {
	return repositories.NewMail(db.NewContext(w.tx), email.NewOutbox(nil))
}

func (w *dueWorld) mailer(sends func(string) bool) notificationsemail.Mailer {
	return notificationsemail.Mailer{Outbox: w.mailStore(), Sends: sends}
}

func (w *dueWorld) mail(n domain.Notice, at time.Time) {
	w.t.Helper()
	if err := w.mailer(nil).Mail(context.Background(), n, at); err != nil {
		w.t.Fatal(err)
	}
}

func (w *dueWorld) outbox(userID string) []queued {
	w.t.Helper()
	rows, err := w.tx.Query(context.Background(), `
		SELECT template, dedupe_key, params::text, deliver_after FROM app.email_outbox
		 WHERE to_user_id = $1::uuid AND sent_at IS NULL AND failed_at IS NULL ORDER BY created_at, id`, userID)
	if err != nil {
		w.t.Fatal(err)
	}
	defer rows.Close()
	var out []queued
	for rows.Next() {
		var q queued
		if err := rows.Scan(&q.template, &q.key, &q.params, &q.deliverAfter); err != nil {
			w.t.Fatal(err)
		}
		out = append(out, q)
	}
	return out
}

func (w *dueWorld) emailSwitch(userID string, event domain.Event, inApp, mail bool) {
	w.t.Helper()
	w.exec(`INSERT INTO app.notification_preferences (user_id, event, in_app, email) VALUES ($1, $2, $3, $4)
		ON CONFLICT (user_id, event) DO UPDATE SET in_app = EXCLUDED.in_app, email = EXCLUDED.email`, userID, string(event), inApp, mail)
}

func flaggedNotice(userID, attemptID, assignmentID string) domain.Notice {
	return domain.Notice{UserID: userID, Kind: domain.AttemptFlagged,
		Params:    domain.Flagged{StudentName: "Nam", Title: "Đề", FocusLost: 2},
		Target:    &domain.Target{Route: domain.RouteAttempt, AttemptID: attemptID, AssignmentID: assignmentID},
		DedupeKey: domain.FlaggedKey(attemptID), Merge: domain.Replace}
}

func submittedNotice(userID, assignmentID string, at time.Time) domain.Notice {
	return domain.Notice{UserID: userID, Kind: domain.AttemptSubmitted,
		Params:    domain.Submitted{Title: "Đề", Count: 1},
		Target:    &domain.Target{Route: domain.RouteAssignment, AssignmentID: assignmentID},
		DedupeKey: domain.SubmittedKey(assignmentID, at), Merge: domain.Add}
}

func readyNotice(userID, attemptID, assignmentID string) domain.Notice {
	return domain.Notice{UserID: userID, Kind: domain.ResultReady, Params: domain.Ready{Title: "Đề"},
		Target:    &domain.Target{Route: domain.RouteResult, AttemptID: attemptID, AssignmentID: assignmentID},
		DedupeKey: domain.ReadyKey(attemptID), Merge: domain.Replace}
}

// TestTheEmailFollowsTheSwitchOrTheDecksDefaultAndAStudentsOneSwitch is the
// preference matrix of T-R7.13: a teacher who saved nothing gets a flag by
// email and not a submission; a saved email column wins either way; a
// student's in-app switch decides their email (DG-82), a row saved before R7
// with email false included; a disabled account gets none; a share, which
// has no switch, always goes.
func TestTheEmailFollowsTheSwitchOrTheDecksDefaultAndAStudentsOneSwitch(t *testing.T) {
	w := newDueWorld(t)
	a := w.assignment(spec{opens: w.now.Add(-2 * time.Hour), closes: w.now.Add(time.Hour)})
	attempt := w.attempt(a, w.student, "graded", w.now.Add(-time.Minute))
	count := func(user string) int { return len(w.outbox(user)) }

	w.mail(flaggedNotice(w.creator, attempt, a), w.now)
	w.mail(submittedNotice(w.creator, a, w.now), w.now)
	if got := w.outbox(w.creator); len(got) != 1 || got[0].template != string(email.AttemptFlagged) {
		t.Fatalf("a teacher with no saved switch has %+v, want the flag's email alone", got)
	}

	other := w.user("teacher")
	w.emailSwitch(other, domain.EventAttemptFlagged, true, false)
	w.emailSwitch(other, domain.EventAttemptSubmitted, true, true)
	w.mail(flaggedNotice(other, attempt, a), w.now)
	w.mail(submittedNotice(other, a, w.now), w.now)
	if got := w.outbox(other); len(got) != 1 || got[0].template != string(email.SubmissionsDigest) {
		t.Errorf("a teacher who turned the flag's email off and the submissions' on has %+v", got)
	}

	w.mail(readyNotice(w.student, attempt, a), w.now)
	if count(w.student) != 1 {
		t.Errorf("a student with no saved switch got %d result emails, want 1", count(w.student))
	}
	off, before := w.user("student"), w.user("student")
	w.emailSwitch(off, domain.EventResultReady, false, true)
	w.emailSwitch(before, domain.EventResultReady, true, false)
	w.mail(readyNotice(off, attempt, a), w.now)
	w.mail(readyNotice(before, w.attempt(a, w.student, "graded", w.now), a), w.now)
	if count(off) != 0 || count(before) != 1 {
		t.Errorf("a student's email must follow their in-app switch: off has %d, a pre-R7 row has %d", count(off), count(before))
	}

	gone := w.user("teacher")
	w.disabled(gone)
	w.mail(flaggedNotice(gone, attempt, a), w.now)
	if count(gone) != 0 {
		t.Errorf("a disabled teacher has %d emails", count(gone))
	}

	w.emailSwitch(other, domain.EventMessageReceived, false, false)
	w.mail(domain.Notice{UserID: other, Kind: domain.ContentShared,
		Params:    domain.Shared{SharerName: "Lan", ContentKind: domain.ShareTest, Name: "Đề", Access: domain.ShareUse},
		Target:    &domain.Target{Route: domain.RouteSharedWithMe},
		DedupeKey: domain.SharedKey(domain.ShareTest, a), Merge: domain.Replace}, w.now)
	if got := w.outbox(other); len(got) != 2 || got[1].template != string(email.ContentShared) {
		t.Errorf("a share, which has no switch, did not email: %+v", got)
	}
}

// TestTheOutboxHoldsIdsAloneAndTheDigestWaitsForItsBucketsEnd fails when a
// notification email's row carries what it says rather than ids, or when the
// papers of one bucket write more than one digest, or one due before the
// bucket's end.
func TestTheOutboxHoldsIdsAloneAndTheDigestWaitsForItsBucketsEnd(t *testing.T) {
	w := newDueWorld(t)
	w.emailSwitch(w.creator, domain.EventAttemptSubmitted, true, true)
	a := w.assignment(spec{opens: w.now.Add(-2 * time.Hour), closes: w.now.Add(time.Hour)})
	start := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	for _, at := range []time.Time{start.Add(time.Minute), start.Add(9 * time.Minute), start.Add(14 * time.Minute)} {
		w.mail(submittedNotice(w.creator, a, at), at)
	}
	got := w.outbox(w.creator)
	if len(got) != 1 || !got[0].deliverAfter.Equal(start.Add(15*time.Minute)) {
		t.Fatalf("three papers in one bucket queued %+v, want one digest due at %v", got, start.Add(15*time.Minute))
	}
	if got[0].params != `{"from": "2026-10-10T12:00:00Z", "assignmentId": "`+a+`"}` {
		t.Errorf("the digest's row holds %s, want the assignment and the bucket alone", got[0].params)
	}
	w.mail(submittedNotice(w.creator, a, start.Add(16*time.Minute)), start.Add(16*time.Minute))
	if n := len(w.outbox(w.creator)); n != 2 {
		t.Errorf("the next bucket's paper queued %d digests in all, want 2", n)
	}
}

// TestTheDigestCountsItsBucketsPapersWhenItIsSent fails when the digest's
// count is the one at enqueue rather than at send, counts a paper outside its
// bucket or one the teacher does not reach, or is sent for a bucket with none.
func TestTheDigestCountsItsBucketsPapersWhenItIsSent(t *testing.T) {
	w := newDueWorld(t)
	a := w.assignment(spec{title: "Đề Unit 5", opens: w.now.Add(-2 * time.Hour), closes: w.now.Add(time.Hour)})
	start := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	others := []string{w.user("student"), w.user("student")}
	for _, s := range others {
		w.join(w.class, s)
	}
	w.attempt(a, w.student, "submitted", start.Add(time.Minute))
	w.pending(w.attempt(a, others[0], "submitted", start.Add(5*time.Minute)))
	w.attempt(a, others[1], "graded", start.Add(14*time.Minute))
	w.attempt(a, w.user("student"), "submitted", start.Add(16*time.Minute))
	store := w.mailStore()
	got, ok, err := store.Digest(context.Background(), w.creator, email.SubmissionsDigestParams{AssignmentID: a, From: start}, w.now)
	if err != nil || !ok || got.Count != 3 || got.ToGrade != 1 || got.Title != "Đề Unit 5" {
		t.Errorf("the digest read %+v (%v %v), want 3 papers in the bucket, 1 to grade", got, ok, err)
	}
	if _, ok, err := store.Digest(context.Background(), w.user("teacher"), email.SubmissionsDigestParams{AssignmentID: a, From: start}, w.now); err != nil || ok {
		t.Errorf("a teacher who reaches none of the papers gets a digest (%v %v)", ok, err)
	}
	if _, ok, err := store.Digest(context.Background(), w.creator, email.SubmissionsDigestParams{AssignmentID: a, From: start.Add(time.Hour)}, w.now); err != nil || ok {
		t.Errorf("a bucket with no paper sends a digest (%v %v)", ok, err)
	}
}

func (w *dueWorld) direct(staff, student string) string {
	w.t.Helper()
	return w.id(`INSERT INTO app.conversations (kind, staff_id, student_id, last_message_at) VALUES ('direct', $1, $2, $3) RETURNING id::text`, staff, student, w.now)
}

func (w *dueWorld) post(conversation, sender string) string {
	w.t.Helper()
	return w.id(`INSERT INTO app.messages (conversation_id, sender_id, client_id, body) VALUES ($1, $2, gen_random_uuid(), 'Em chào cô ạ') RETURNING id::text`, conversation, sender)
}

func messageNotice(teacher, student, conversation string) domain.Notice {
	return domain.Notice{UserID: teacher, Kind: domain.MessageReceived,
		Params:    domain.MessageNotice{SenderName: "Hà My", Count: 1, Excerpt: "Em chào cô ạ"},
		Target:    &domain.Target{Route: domain.RouteMessages, StudentID: student},
		DedupeKey: domain.MessageKey(conversation), Merge: domain.Renew}
}

// TestAMessageEmailWaitsTenMinutesAndIsDroppedOnceTheThreadIsRead fails when
// a message's email is due before ten minutes, carries the message, or is
// still sent after the reader caught up with the thread.
func TestAMessageEmailWaitsTenMinutesAndIsDroppedOnceTheThreadIsRead(t *testing.T) {
	w := newDueWorld(t)
	thread := w.direct(w.creator, w.student)
	message := w.post(thread, w.student)
	w.mail(messageNotice(w.creator, w.student, thread), w.now)
	got := w.outbox(w.creator)
	if len(got) != 1 || got[0].template != string(email.MessageReceived) || !got[0].deliverAfter.Equal(w.now.Add(10*time.Minute)) {
		t.Fatalf("the message queued %+v, want one email due ten minutes later", got)
	}
	if got[0].params != `{"studentId": "`+w.student+`", "conversationId": "`+thread+`"}` {
		t.Errorf("the message email's row holds %s, want the thread and its route alone", got[0].params)
	}
	store := w.mailStore()
	params := email.MessageReceivedParams{ConversationID: thread, StudentID: w.student}
	filled, ok, err := store.Message(context.Background(), w.creator, params, w.now)
	if err != nil || !ok || filled.Count != 1 || filled.SenderName != "Thành viên" || filled.ClassName != "" {
		t.Errorf("an unread thread reads %+v (%v %v)", filled, ok, err)
	}
	w.exec(`INSERT INTO app.conversation_reads (conversation_id, user_id, last_read_message_id) VALUES ($1, $2, $3)`, thread, w.creator, message)
	if _, ok, err := store.Message(context.Background(), w.creator, params, w.now); err != nil || ok {
		t.Errorf("a thread read since is still sent (%v %v)", ok, err)
	}
	w.post(thread, w.creator)
	if _, ok, err := store.Message(context.Background(), w.creator, params, w.now); err != nil || ok {
		t.Errorf("the reader's own reply makes the thread unread for them (%v %v)", ok, err)
	}
	if _, ok, err := store.Message(context.Background(), w.user("teacher"), params, w.now); err != nil || ok {
		t.Errorf("someone outside the thread is sent its email (%v %v)", ok, err)
	}
}

// TestAThreadEmailsOneReaderAtMostOnceInThirtyMinutes fails when a second
// message writes a second email while one waits, when the email after a sent
// one is due sooner than thirty minutes after it, or when one a Preparer
// dropped holds back the next.
func TestAThreadEmailsOneReaderAtMostOnceInThirtyMinutes(t *testing.T) {
	w := newDueWorld(t)
	thread := w.direct(w.creator, w.student)
	w.post(thread, w.student)
	w.mail(messageNotice(w.creator, w.student, thread), w.now)
	w.mail(messageNotice(w.creator, w.student, thread), w.now.Add(time.Minute))
	first := w.outbox(w.creator)
	if len(first) != 1 {
		t.Fatalf("two messages while the first email waits queued %d", len(first))
	}
	sent := w.now.Add(10 * time.Minute)
	w.exec(`UPDATE app.email_outbox SET sent_at = $2 WHERE dedupe_key = $1`, first[0].key, sent)
	w.mail(messageNotice(w.creator, w.student, thread), sent.Add(2*time.Minute))
	second := w.outbox(w.creator)
	if len(second) != 1 || second[0].key == first[0].key || !second[0].deliverAfter.Equal(sent.Add(30*time.Minute)) {
		t.Fatalf("a message two minutes after an email went queued %+v, want one due thirty minutes after %v", second, sent)
	}
	skipped := sent.Add(30 * time.Minute)
	w.exec(`UPDATE app.email_outbox SET failed_at = $2, last_error_code = 'SKIPPED' WHERE dedupe_key = $1`, second[0].key, skipped)
	at := skipped.Add(time.Minute)
	w.mail(messageNotice(w.creator, w.student, thread), at)
	third := w.outbox(w.creator)
	if len(third) != 1 || !third[0].deliverAfter.Equal(at.Add(10*time.Minute)) {
		t.Errorf("after a dropped email the next message queued %+v, want one due ten minutes later", third)
	}
}

// TestThePreparersDropWhatIsNoLongerTrue fails when an email is still sent
// after what it says stopped being so: an unflagged or voided paper, a revoked
// grant, a class that changed hands again, a reminder for a paper handed in.
func TestThePreparersDropWhatIsNoLongerTrue(t *testing.T) {
	w := newDueWorld(t)
	ctx := context.Background()
	store := w.mailStore()
	a := w.assignment(spec{opens: w.now.Add(-2 * time.Hour), closes: w.now.Add(time.Hour), maxAttempts: 2})
	attempt := w.attempt(a, w.student, "submitted", w.now.Add(-time.Minute))
	w.exec(`UPDATE app.attempts SET flagged = true, focus_loss_count = 4 WHERE id = $1`, attempt)
	flag := email.AttemptFlaggedParams{AttemptID: attempt}
	if got, ok, err := store.Flagged(ctx, w.creator, flag, w.now); err != nil || !ok || got.FocusLost != 4 || got.Title == "" {
		t.Errorf("a flagged paper reads %+v (%v %v)", got, ok, err)
	}
	if _, ok, _ := store.Flagged(ctx, w.user("teacher"), flag, w.now); ok {
		t.Error("a teacher who does not reach the paper is sent its flag")
	}
	w.exec(`UPDATE app.attempts SET flagged = false WHERE id = $1`, attempt)
	if _, ok, _ := store.Flagged(ctx, w.creator, flag, w.now); ok {
		t.Error("an unflagged paper is still sent as flagged")
	}

	soon := email.AssignmentDueSoonParams{AssignmentID: a, Lead: email.LeadHour}
	late := w.user("student")
	w.join(w.class, late)
	if got, ok, err := store.DueSoon(ctx, late, soon, w.now); err != nil || !ok || !got.ClosesAt.Equal(w.now.Add(time.Hour)) {
		t.Errorf("a reminder for a paper not handed in reads %+v (%v %v)", got, ok, err)
	}
	if _, ok, _ := store.DueSoon(ctx, w.student, soon, w.now); ok {
		t.Error("a reminder is sent to a student who handed the paper in")
	}
	if _, ok, _ := store.DueSoon(ctx, late, soon, w.now.Add(2*time.Hour)); ok {
		t.Error("a reminder held past the close is still sent")
	}

	grantee := w.user("teacher")
	w.exec(`INSERT INTO app.content_shares (kind, test_id, grantee_id, access, granted_by, message)
		VALUES ('test', (SELECT test_id FROM app.assignments WHERE id = $1), $2, 'edit', $3, 'Cô xem giúp em')`, a, grantee, w.creator)
	share := email.ContentSharedParams{Kind: email.SharedTest, ResourceID: w.id(`SELECT test_id::text FROM app.assignments WHERE id = $1`, a)}
	got, ok, err := store.Shared(ctx, grantee, share, w.now)
	if err != nil || !ok || got.Access != email.SharedEdit || got.Message == nil || *got.Message != "Cô xem giúp em" || got.SharerName == "" {
		t.Errorf("a standing grant reads %+v (%v %v)", got, ok, err)
	}
	w.exec(`DELETE FROM app.content_shares WHERE grantee_id = $1`, grantee)
	if _, ok, _ := store.Shared(ctx, grantee, share, w.now); ok {
		t.Error("a revoked grant is still sent as shared")
	}

	next := w.user("teacher")
	w.exec(`UPDATE app.classes SET teacher_id = $2 WHERE id = $1`, w.class, next)
	if got, ok, _ := store.TeacherChanged(ctx, next, email.ClassTeacherChangedParams{ClassID: w.class}, w.now); !ok || got.ClassName != "Lớp thử" {
		t.Errorf("the new teacher's email reads %+v (%v)", got, ok)
	}
	if got, ok, _ := store.TeacherChanged(ctx, w.creator, email.ClassTeacherChangedParams{ClassID: w.class, Previous: true}, w.now); !ok || got.TeacherName != "Thành viên" {
		t.Errorf("the previous teacher's email reads %+v (%v)", got, ok)
	}
	w.exec(`UPDATE app.classes SET teacher_id = $2 WHERE id = $1`, w.class, w.creator)
	if _, ok, _ := store.TeacherChanged(ctx, next, email.ClassTeacherChangedParams{ClassID: w.class}, w.now); ok {
		t.Error("a class that changed hands again still tells its next teacher they teach it")
	}
	if _, ok, _ := store.TeacherChanged(ctx, w.creator, email.ClassTeacherChangedParams{ClassID: w.class, Previous: true}, w.now); ok {
		t.Error("a teacher who has the class back is told it is taught by another")
	}
}

// TestAPreparerDropsTheEmailOnceTheSwitchIsTurnedOffOrTheKillSwitchNamesIt
// runs the real Preparers on a queued result email: sent as written, dropped
// once the student turns the switch off, and dropped while
// NOTIFY_EMAIL_EVENTS leaves result.ready out.
func TestAPreparerDropsTheEmailOnceTheSwitchIsTurnedOffOrTheKillSwitchNamesIt(t *testing.T) {
	w := newDueWorld(t)
	a := w.assignment(spec{title: "Đề Unit 5", opens: w.now.Add(-2 * time.Hour), closes: w.now.Add(time.Hour)})
	attempt := w.attempt(a, w.student, "graded", w.now.Add(-time.Minute))
	draft := email.Draft{ToUserID: w.student, Params: email.ResultReadyParams{AttemptID: attempt}, Now: w.now}
	prepare := func(sends func(string) bool) (email.Params, bool) {
		t.Helper()
		p, ok, err := adapters.NotificationPreparers(w.mailStore(), sends)[email.ResultReady].Prepare(context.Background(), draft)
		if err != nil {
			t.Fatal(err)
		}
		return p, ok
	}
	if p, ok := prepare(nil); !ok || p.(email.ResultReadyParams).Title != "Đề Unit 5" || p.(email.ResultReadyParams).FullName != "Thành viên" {
		t.Errorf("the result email prepares as %+v (%v)", p, ok)
	}
	if _, ok := prepare(func(e string) bool { return e != "result.ready" }); ok {
		t.Error("NOTIFY_EMAIL_EVENTS without result.ready still sends it")
	}
	w.prefer(w.student, domain.EventResultReady, false)
	if _, ok := prepare(nil); ok {
		t.Error("a student who turned the switch off since is still sent the email")
	}
}
