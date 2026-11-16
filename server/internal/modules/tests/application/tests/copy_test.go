//go:build integration

package application_test

import (
	"context"
	"testing"
	"time"

	"quizzivy/internal/core/adapters"
	"quizzivy/internal/modules/tests/domain"
)

// draftRows is what the test's draft names: its standalone bank questions
// and its own groups, in id order.
func (w *shareWorld) draftRows(t *testing.T, testID string) (questions, groups []string) {
	t.Helper()
	scan := func(sql string) []string {
		rows, err := w.tx.Query(context.Background(), sql, testID)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				t.Fatal(err)
			}
			out = append(out, id)
		}
		return out
	}
	return scan(`SELECT sq.question_id::text FROM app.test_section_questions sq JOIN app.test_sections s ON s.id = sq.test_section_id WHERE s.test_id = $1 ORDER BY 1`),
		scan(`SELECT g.id::text FROM app.question_groups g JOIN app.test_sections s ON s.id = g.owner_section_id WHERE s.test_id = $1 ORDER BY 1`)
}

func (w *shareWorld) count(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := w.tx.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestACopyOfATestIsTheCopiersOwnDraftAndLeavesEverythingElseBehind holds
// Make a copy to the ContentAccess table (any reader copies, a stranger and a
// revoked grantee are answered as for a missing test) and to what it copies:
// B's copy of A's published test is a draft of B's, titled as A's, whose
// sections hold a new question of B's in place of A's and a new group in
// place of A's section group, with none of A's versions, assignments or
// grants, audited as a test B created and as share.copied from A's. A's test
// is unchanged, and A's own duplicate still references A's question.
func TestACopyOfATestIsTheCopiersOwnDraftAndLeavesEverythingElseBehind(t *testing.T) {
	for _, level := range []string{levelNone, levelUse, levelEdit, levelRevoked} {
		t.Run(level, func(t *testing.T) {
			ctx := context.Background()
			w := newShareWorld(t, level)
			w.tests.WithQuestionWrites(adapters.QuestionWrites{})
			source := w.current(t, w.testA)
			sourceQuestions, sourceGroups := w.draftRows(t, w.testA)

			copied, err := w.tests.Copy(ctx, domain.DuplicateInput{ID: w.testA, ActorID: w.b, Scope: w.scopeB, Now: time.Now()})
			w.check(t, "copy", reads, err)
			if err != nil {
				return
			}
			if copied.OwnerID != w.b || copied.Access != domain.AccessOwner || copied.Title != source.Title || copied.Status != domain.Draft ||
				copied.CurrentVersion != 0 || copied.SharedWith != 0 || len(copied.Sections) != len(source.Sections) {
				t.Fatalf("the copy reads %+v; want B's own draft titled %q with %d sections, no version and no grant", copied, source.Title, len(source.Sections))
			}
			if versions, err := w.tests.ListVersions(ctx, w.scopeB, copied.ID); err != nil || len(versions) != 0 {
				t.Errorf("the copy lists versions %v (%v), want none", versions, err)
			}
			if n := w.count(t, `SELECT count(*) FROM app.content_shares WHERE test_id = $1`, copied.ID); n != 0 {
				t.Errorf("the copy has %d grants, want none", n)
			}
			if n := w.count(t, `SELECT count(*) FROM app.assignments a JOIN app.test_versions v ON v.id = a.test_version_id WHERE v.test_id = $1`, copied.ID); n != 0 {
				t.Errorf("the copy has %d assignments, want none", n)
			}
			questions, groups := w.draftRows(t, copied.ID)
			if len(questions) != len(sourceQuestions) || len(groups) != len(sourceGroups) || len(groups) == 0 {
				t.Fatalf("the copy's draft names questions %v and groups %v, want as many as A's %v and %v", questions, groups, sourceQuestions, sourceGroups)
			}
			for _, id := range questions {
				if id == w.questionA {
					t.Fatalf("the copy's draft names A's question %s", id)
				}
				if n := w.count(t, `SELECT count(*) FROM app.questions WHERE id = $1 AND owner_id = $2 AND created_by = $2`, id, w.b); n != 1 {
					t.Errorf("the copy's question %s is not B's own", id)
				}
			}
			for _, id := range groups {
				if id == w.sectionGroupA.Bundle.Group.ID {
					t.Fatalf("the copy's draft names A's group %s", id)
				}
			}
			if n := w.count(t, `SELECT count(*) FROM app.audit_log WHERE action = 'share.copied' AND entity = 'test' AND entity_id = $1
				AND actor_user_id = $2 AND diff->>'sourceId' = $3`, copied.ID, w.b, w.testA); n != 1 {
				t.Errorf("the copy has %d share.copied rows from A's test, want one", n)
			}
			if n := w.count(t, `SELECT count(*) FROM app.audit_log WHERE action = 'test.created' AND entity = 'test' AND entity_id = $1
				AND actor_user_id = $2`, copied.ID, w.b); n != 1 {
				t.Errorf("the copy has %d test.created rows by B, want one beside share.copied", n)
			}

			afterQuestions, afterGroups := w.draftRows(t, w.testA)
			if len(afterQuestions) != 1 || afterQuestions[0] != w.questionA || len(afterGroups) != len(sourceGroups) {
				t.Errorf("A's draft names %v and %v after the copy, want A's question and groups unchanged", afterQuestions, afterGroups)
			}
			duplicated, err := w.tests.Duplicate(ctx, domain.DuplicateInput{ID: w.testA, ActorID: w.a, Scope: w.scopeA, Now: time.Now()})
			if err != nil {
				t.Fatal(err)
			}
			if referenced, _ := w.draftRows(t, duplicated.ID); len(referenced) != 1 || referenced[0] != w.questionA {
				t.Errorf("A's duplicate names %v, want A's own question: duplicate keeps its references", referenced)
			}
		})
	}
}

// TestACopyOfATestKeepsTheMediaOfItsGroups: B, with Can use, copies A's test
// whose section group plays A's audio. The copy's group binds the same audio
// by asset id: B reads it through the share, as the media module defines a
// readable asset, so the copy's group write accepts it.
func TestACopyOfATestKeepsTheMediaOfItsGroups(t *testing.T) {
	ctx := context.Background()
	w := newShareWorld(t, levelUse)
	w.tests.WithQuestionWrites(adapters.QuestionWrites{})
	audio := storedGroupAsset(t, w.tx, w.a, "audio")
	if _, err := w.groups.Create(ctx, domain.CreateGroupInput{Bundle: storedGroupFixture(t, audio), OwnerSectionID: &w.sectionA,
		ExpectedTestUpdatedAt: w.current(t, w.testA).UpdatedAt, ActorID: w.a, Now: time.Now(), Scope: w.scopeA, Grants: bothKeys}); err != nil {
		t.Fatal(err)
	}
	copied, err := w.tests.Copy(ctx, domain.DuplicateInput{ID: w.testA, ActorID: w.b, Scope: w.scopeB, Now: time.Now()})
	if err != nil {
		t.Fatalf("B copying A's test with A's audio group: %v", err)
	}
	if n := w.count(t, `SELECT count(*) FROM app.group_recordings r JOIN app.question_groups g ON g.id = r.group_id
		JOIN app.test_sections s ON s.id = g.owner_section_id WHERE s.test_id = $1 AND r.media_asset_id = $2`, copied.ID, audio); n != 1 {
		t.Errorf("the copy's groups play A's audio %d times, want once", n)
	}
}
