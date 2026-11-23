//go:build integration

package application_test

import (
	"context"
	"errors"
	"testing"

	"quizzivy/internal/modules/classes/application/command"
	"quizzivy/internal/modules/classes/domain"
)

func TestARefusedClassDeletionNamesWhatHoldsIt(t *testing.T) {
	w := newClassWorld(t)
	w.sat(t, w.a, w.classA, w.studentA)
	w.exec(t, `UPDATE app.classes SET archived_at = now() WHERE id = $1`, w.classA)
	_, err := w.svc.Commands.Delete.Handle(context.Background(), command.Delete{ClassID: w.classA, Actor: w.who(w.a)})
	var refused *domain.ReferencedError
	if !errors.Is(err, domain.ErrReferenced) || !errors.As(err, &refused) || refused.By != domain.ReferencedByAssignments {
		t.Fatalf("deleting a class an assignment targets answered %v, want referenced by assignments", err)
	}
	var kept bool
	if err := w.tx.QueryRow(context.Background(), `SELECT EXISTS (SELECT 1 FROM app.classes WHERE id = $1)`, w.classA).Scan(&kept); err != nil || !kept {
		t.Fatalf("the class was deleted (%v)", err)
	}
}

// TestDeletingAClassWithAPostedChannelNamesItsMessages keeps T-R7.9's
// RESTRICT key from a class's channel mapped: the refusal is a 409 naming
// messages, never a 500, and the class and its posts stay.
func TestDeletingAClassWithAPostedChannelNamesItsMessages(t *testing.T) {
	w := newClassWorld(t)
	channel := w.id(t, `INSERT INTO app.conversations (kind, class_id, last_message_at) VALUES ('class', $1, now()) RETURNING id::text`, w.classA)
	w.exec(t, `INSERT INTO app.messages (conversation_id, sender_id, client_id, body) VALUES ($1, $2, uuidv7(), 'Lịch nghỉ lễ')`, channel, w.a)
	w.exec(t, `UPDATE app.classes SET archived_at = now() WHERE id = $1`, w.classA)
	_, err := w.svc.Commands.Delete.Handle(context.Background(), command.Delete{ClassID: w.classA, Actor: w.who(w.a)})
	var refused *domain.ReferencedError
	if !errors.Is(err, domain.ErrReferenced) || !errors.As(err, &refused) || refused.By != domain.ReferencedByMessages {
		t.Fatalf("deleting a class whose channel has posts answered %v, want referenced by messages", err)
	}
	if got := w.id(t, `SELECT (SELECT count(*) FROM app.classes WHERE id = $1) || '|' || (SELECT count(*) FROM app.messages WHERE conversation_id = $2)`, w.classA, channel); got != "1|1" {
		t.Errorf("the class and its posts are %s, want 1|1", got)
	}
}

// TestDeletingAClassWhoseOnlyReferenceIsAttendanceNamesIt keeps T-R8.2's
// RESTRICT key from a mark to its session mapped: the class's delete reaches
// the mark through the cascade to its sessions, and the refusal is a 409
// naming attendance, never a 500. The class, its sessions and the mark stay.
// A class with sessions but no attendance deletes, and its sessions with it.
func TestDeletingAClassWhoseOnlyReferenceIsAttendanceNamesIt(t *testing.T) {
	w := newClassWorld(t)
	series := w.id(t, `INSERT INTO app.class_session_series (class_id, weekday, start_time, duration_minutes, starts_on, time_zone, created_by)
		VALUES ($1, 1, '18:00', 90, DATE '2026-09-07', 'Asia/Ho_Chi_Minh', $2) RETURNING id::text`, w.classA, w.a)
	session := w.id(t, `INSERT INTO app.class_sessions (class_id, series_id, series_date, starts_at, duration_minutes, created_by)
		VALUES ($1, $2, DATE '2026-09-14', TIMESTAMPTZ '2026-09-14 18:00+07', 90, $3) RETURNING id::text`, w.classA, series, w.a)
	w.exec(t, `INSERT INTO app.class_sessions (class_id, starts_at, duration_minutes, created_by) VALUES ($1, now(), 90, $2)`, w.classA, w.a)
	w.exec(t, `INSERT INTO app.attendance_marks (session_id, student_id, status, marked_by, marked_at) VALUES ($1, $2, 'late', $3, now())`, session, w.studentA, w.a)
	// The member is the class's only other row, and members cascade: the mark
	// is what refuses.
	w.exec(t, `UPDATE app.classes SET archived_at = now() WHERE id = $1`, w.classA)
	_, err := w.svc.Commands.Delete.Handle(context.Background(), command.Delete{ClassID: w.classA, Actor: w.who(w.a)})
	var refused *domain.ReferencedError
	if !errors.Is(err, domain.ErrReferenced) || !errors.As(err, &refused) || refused.By != domain.ReferencedByAttendance {
		t.Fatalf("deleting a class whose session has attendance answered %v, want referenced by attendance", err)
	}
	if got := w.id(t, `SELECT (SELECT count(*) FROM app.classes WHERE id = $1) || '|' || (SELECT count(*) FROM app.class_sessions WHERE class_id = $1)
		|| '|' || (SELECT count(*) FROM app.attendance_marks WHERE session_id = $2)`, w.classA, session); got != "1|2|1" {
		t.Errorf("the class, its sessions and the mark are %s, want 1|2|1", got)
	}

	w.exec(t, `INSERT INTO app.class_session_series (class_id, weekday, start_time, duration_minutes, starts_on, time_zone, created_by)
		VALUES ($1, 3, '08:00', 60, DATE '2026-09-09', 'Asia/Ho_Chi_Minh', $2)`, w.classB, w.b)
	w.exec(t, `INSERT INTO app.class_sessions (class_id, starts_at, duration_minutes, created_by) VALUES ($1, now(), 90, $2)`, w.classB, w.b)
	w.exec(t, `UPDATE app.classes SET archived_at = now() WHERE id = $1`, w.classB)
	if _, err := w.svc.Commands.Delete.Handle(context.Background(), command.Delete{ClassID: w.classB, Actor: w.who(w.b)}); err != nil {
		t.Fatalf("deleting a class with sessions but no attendance answered %v", err)
	}
	if got := w.id(t, `SELECT (SELECT count(*) FROM app.class_session_series WHERE class_id = $1) || '|' || (SELECT count(*) FROM app.class_sessions WHERE class_id = $1)`, w.classB); got != "0|0" {
		t.Errorf("the deleted class left series|sessions %s, want 0|0", got)
	}
}
