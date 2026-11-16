//go:build integration

package application_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/google/uuid"

	"quizzivy/internal/modules/questions/application/command"
	"quizzivy/internal/modules/questions/domain"
	"quizzivy/internal/shared/access"
)

func (w *bankWorld) grantTest(t *testing.T, testID, grantee, level string) {
	t.Helper()
	if _, err := w.tx.Exec(context.Background(), `INSERT INTO app.content_shares (kind, test_id, grantee_id, access, granted_by)
		VALUES ('test', $1::uuid, $2::uuid, $3, (SELECT owner_id FROM app.tests WHERE id = $1::uuid))`, testID, grantee, level); err != nil {
		t.Fatal(err)
	}
}

func (w *bankWorld) revokeTest(t *testing.T, testID, grantee string) {
	t.Helper()
	if _, err := w.tx.Exec(context.Background(), `DELETE FROM app.content_shares WHERE test_id = $1::uuid AND grantee_id = $2::uuid`, testID, grantee); err != nil {
		t.Fatal(err)
	}
}

// TestABankQuestionIsReadThroughTheTestThatHoldsIt holds the question-side
// predicate to each level of a grant to the test whose draft holds it: B
// reads A's question with the grant's access and A as its owner, may not
// change it outside the test, delete it or duplicate it, and, with no grant
// or a revoked one, gets exactly what a missing question answers.
func TestABankQuestionIsReadThroughTheTestThatHoldsIt(t *testing.T) {
	for _, level := range []string{"none", "use", "edit", "revoked"} {
		t.Run(level, func(t *testing.T) {
			w := newSetWorld(t)
			ctx := context.Background()
			question := w.create(t, w.a, "Câu trong đề được chia sẻ", nil)
			test := w.draftUsing(t, w.a, question.ID)
			switch level {
			case "use", "edit":
				w.grantTest(t, test, w.b, level)
			case "revoked":
				w.grantTest(t, test, w.b, "edit")
				w.revokeTest(t, test, w.b)
			}
			scopeB := access.Scope{UserID: w.b}
			readable := level == "use" || level == "edit"

			got, err := w.get(scopeB, question.ID)
			switch {
			case readable && err != nil:
				t.Fatalf("B reading A's question at %s: %v", level, err)
			case readable && (string(got.Access) != level || got.Owner == nil || got.Owner.ID != w.a):
				t.Errorf("B reads access %q and owner %+v, want %s and A", got.Access, got.Owner, level)
			case readable && !slices.ContainsFunc(got.UsedIn, func(ref domain.TestRef) bool { return ref.ID == test }):
				t.Errorf("B's read of the question names its uses %+v, which leave out the test shared with B", got.UsedIn)
			case !readable && !errors.Is(err, domain.ErrNotFound):
				t.Errorf("B reading A's question at %s: %v, want not found", level, err)
			}

			wantWrite := map[string]error{"none": domain.ErrNotFound, "revoked": domain.ErrNotFound, "use": domain.ErrReadOnly, "edit": domain.ErrEditContextRequired}[level]
			_, err = w.svc.Commands.Update.Handle(ctx, command.Update{Request: request(w.b, false, question.ID, w.input("B sửa ngoài đề", nil))})
			if !errors.Is(err, wantWrite) {
				t.Errorf("B's edit outside the test: %v, want %v", err, wantWrite)
			}
			wantOwner := domain.ErrOwnerOnly
			if !readable {
				wantOwner = domain.ErrNotFound
			}
			_, err = w.svc.Commands.Delete.Handle(ctx, command.Delete{Request: request(w.b, false, question.ID, domain.Input{})})
			if !errors.Is(err, wantOwner) {
				t.Errorf("B deleting A's question: %v, want %v", err, wantOwner)
			}
			_, err = w.svc.Commands.Duplicate.Handle(ctx, command.Duplicate{Request: request(w.b, false, question.ID, domain.Input{})})
			if !errors.Is(err, wantOwner) {
				t.Errorf("B duplicating A's question: %v, want %v", err, wantOwner)
			}
			if _, deleted, _ := w.owner(t, question.ID); deleted != "" {
				t.Error("A's question was deleted by B")
			}
			if again, err := w.get(access.Scope{UserID: w.a}, question.ID); err != nil || again.Access != domain.AccessOwner || again.Prompt != question.Prompt {
				t.Errorf("A's own read after B's attempts: %+v, %v", again, err)
			}
		})
	}
}

// TestAQuestionInAnEditableSetReadsAsEdit: a set's grant opens its questions
// as the set does, and the best of several grants wins.
func TestAQuestionInAnEditableSetReadsAsEdit(t *testing.T) {
	w := newSetWorld(t)
	question := w.create(t, w.a, "Câu trong bộ được chia sẻ", nil)
	set := w.makeSet(t, w.a, "Bộ được chia sẻ", question.ID)
	test := w.draftUsing(t, w.a, question.ID)
	w.grantTest(t, test, w.b, "use")
	scopeB := access.Scope{UserID: w.b}
	if got, err := w.get(scopeB, question.ID); err != nil || got.Access != domain.AccessUse {
		t.Fatalf("B through a Can use test: %q, %v", got.Access, err)
	}
	if _, err := w.tx.Exec(context.Background(), `INSERT INTO app.content_shares (kind, question_set_id, grantee_id, access, granted_by)
		VALUES ('question_set', $1::uuid, $2::uuid, 'edit', $3::uuid)`, set.ID, w.b, w.a); err != nil {
		t.Fatal(err)
	}
	if got, err := w.get(scopeB, question.ID); err != nil || got.Access != domain.AccessEdit {
		t.Errorf("B through a Can use test and a Can edit set: %q, %v, want edit", got.Access, err)
	}
	if got, err := w.get(access.Scope{UserID: w.admin, All: true}, question.ID); err != nil || got.Access != domain.AccessOwner {
		t.Errorf("scope.all reads %q, %v, want owner", got.Access, err)
	}
}

// TestAQuestionMadeForASharedTestNeedsEditAccess holds forTestId to the tests
// module's edit predicate: an edit grantee's question lands in the owner's
// bank with the grantee as its author, a Can use grantee gets
// ErrTestReadOnly, and anyone who cannot read the test ErrTestNotFound.
func TestAQuestionMadeForASharedTestNeedsEditAccess(t *testing.T) {
	w := newSetWorld(t)
	ctx := context.Background()
	anchor := w.create(t, w.a, "Câu giữ đề", nil)
	used, edited := w.draftUsing(t, w.a, anchor.ID), w.draftUsing(t, w.a, anchor.ID)
	w.grantTest(t, used, w.b, "use")
	w.grantTest(t, edited, w.b, "edit")

	for label, c := range map[string]struct {
		test string
		want error
	}{
		"a Can use test":   {used, domain.ErrTestReadOnly},
		"an unshared test": {w.draftUsing(t, w.a, anchor.ID), domain.ErrTestNotFound},
		"a missing test":   {uuid.NewString(), domain.ErrTestNotFound},
	} {
		_, err := w.svc.Commands.Create.Handle(ctx, command.Create{Request: request(w.b, false, "", w.input("Câu cho "+label, nil)), ForTestID: c.test})
		if !errors.Is(err, c.want) {
			t.Errorf("B making a question for %s: %v, want %v", label, err, c.want)
		}
	}
	made, err := w.svc.Commands.Create.Handle(ctx, command.Create{Request: request(w.b, false, "", w.input("Câu B làm cho đề", nil)), ForTestID: edited})
	if err != nil {
		t.Fatal(err)
	}
	var owner, author string
	if err := w.tx.QueryRow(ctx, `SELECT owner_id::text, created_by::text FROM app.questions WHERE id = $1`, made.ID).Scan(&owner, &author); err != nil {
		t.Fatal(err)
	}
	if owner != w.a || author != w.b || made.Access != domain.AccessEdit || made.Owner == nil || made.Owner.ID != w.a {
		t.Errorf("B's question for the Can edit test: owner %s, author %s, access %q, owner read %+v; want A, B, edit, A", owner, author, made.Access, made.Owner)
	}
	own := w.create(t, w.b, "Câu của B", nil)
	copied, err := w.svc.Commands.Duplicate.Handle(ctx, command.Duplicate{Request: request(w.b, false, own.ID, domain.Input{}), ForTestID: edited})
	if err != nil {
		t.Fatal(err)
	}
	if o, _, _ := w.owner(t, copied.ID); o != w.a {
		t.Errorf("B's copy for the Can edit test is owned by %s, want A", o)
	}
	if _, err := w.svc.Commands.Duplicate.Handle(ctx, command.Duplicate{Request: request(w.b, false, own.ID, domain.Input{}), ForTestID: used}); !errors.Is(err, domain.ErrTestReadOnly) {
		t.Errorf("B's copy for the Can use test: %v, want ErrTestReadOnly", err)
	}
}
