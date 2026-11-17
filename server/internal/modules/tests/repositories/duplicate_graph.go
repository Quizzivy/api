package repositories

import (
	"context"
	"quizzivy/internal/modules/tests/domain"
	"quizzivy/internal/platform/db"
	"quizzivy/internal/shared/access"
	"time"

	"github.com/jackc/pgx/v5"
)

// copyDraftGraph writes draft into the new test testID owned by owner: its
// sections and instructions, its own groups as new groups, and its standalone
// questions as questions says, a source's id to the id the copy names (the
// source itself for a duplicate, which keeps referencing the bank; a copy of
// it for Make a copy). Every question the copy names is locked with
// LockForDraftUse and held to R2's rule that owner owns it before the
// outline names it.
func (s *Postgres) copyDraftGraph(ctx context.Context, tx pgx.Tx, testID, owner string, draft domain.DraftContent, in domain.DuplicateInput, questions func(string) string) error {
	protected := domain.DraftContent{}
	for _, section := range draft.Sections {
		owned := domain.DraftSection{Groups: section.Groups}
		for _, group := range section.Groups {
			for _, question := range group.Questions {
				owned.Questions = append(owned.Questions, draftGroupQuestion(question, len(owned.Questions)))
			}
		}
		protected.Sections = append(protected.Sections, owned)
	}
	missing, err := lockMediaAssets(ctx, tx, protected, s.media)
	if err != nil {
		return err
	}
	if len(missing) > 0 {
		return goneAsset(missing[0])
	}
	for _, section := range draft.Sections {
		if err := s.copyDraftSection(ctx, tx, testID, owner, section, in, questions); err != nil {
			return err
		}
	}
	return nil
}

func (s *Postgres) copyDraftSection(ctx context.Context, tx pgx.Tx, testID, owner string, section domain.DraftSection, in domain.DuplicateInput, copyOf func(string) string) error {
	var sectionID string
	if err := tx.QueryRow(ctx, `INSERT INTO app.test_sections (test_id,ordinal,title,instructions) VALUES ($1,$2,$3,$4) RETURNING id::text`, testID, section.Ordinal, section.Title, section.Instructions).Scan(&sectionID); err != nil {
		return err
	}
	sources := standaloneDraftIDs(section)
	ids := make([]string, len(sources))
	questions := make(map[string]string, len(sources))
	for i, source := range sources {
		ids[i] = copyOf(source)
		questions[source] = ids[i]
	}
	if err := s.lockQuestions(ctx, tx, owner, []domain.SectionInput{{QuestionIDs: ids}}); err != nil {
		return err
	}
	if err := writeSectionQuestions(ctx, tx, sectionID, ids); err != nil {
		return err
	}
	if len(section.Units) == 0 {
		return nil
	}
	groups := NewGroupsPostgres(db.NewContext(tx), s.groupQuestions, s.media)
	copied := make(map[string]string, len(section.Groups))
	for _, group := range section.Groups {
		bundle, err := detachedGroupCopy(group)
		if err != nil {
			return err
		}
		var updated time.Time
		if err := tx.QueryRow(ctx, `SELECT updated_at FROM app.tests WHERE id=$1`, testID).Scan(&updated); err != nil {
			return err
		}
		stored, err := groups.Create(ctx, domain.CreateGroupInput{Bundle: bundle, OwnerSectionID: &sectionID, ExpectedTestUpdatedAt: updated,
			ActorID: in.ActorID, Now: in.Now, IP: in.IP, UserAgent: in.UserAgent, Scope: in.Scope, Grants: access.NewSet(access.ContentTestsWrite)})
		if err != nil {
			return err
		}
		copied[group.Group.ID] = stored.Bundle.Group.ID
	}
	return replaceCopiedUnits(ctx, tx, sectionID, section.Units, questions, copied)
}

func standaloneDraftIDs(section domain.DraftSection) []string {
	ids := []string{}
	if len(section.Units) == 0 {
		for _, question := range section.Questions {
			ids = append(ids, question.SourceID)
		}
		return ids
	}
	for _, unit := range section.Units {
		if unit.QuestionID != "" {
			ids = append(ids, unit.QuestionID)
		}
	}
	return ids
}
