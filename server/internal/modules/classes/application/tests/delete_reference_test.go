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
