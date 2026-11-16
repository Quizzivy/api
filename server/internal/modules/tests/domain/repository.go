package domain

import (
	"context"
	"quizzivy/internal/shared/access"
	"quizzivy/internal/shared/paging"
	"time"
)

// Repository persists tests, their drafts and the versions published from them.
type Repository interface {
	List(ctx context.Context, in ListInput) ([]Test, paging.Page, error)
	Facets(ctx context.Context, in ListInput) (StatusFacets, error)
	Tags(ctx context.Context, in ListInput) ([]string, error)
	Get(ctx context.Context, scope access.Scope, id string) (Test, error)
	Ownership(ctx context.Context, scope access.Scope, id string) (Ownership, error)
	Create(ctx context.Context, in CreateInput) (Test, error)
	Update(ctx context.Context, in UpdateRequest) (Test, error)
	Duplicate(ctx context.Context, in DuplicateInput) (Test, error)
	// Copy makes the actor's own, independent copy of a test they can read
	// ("Make a copy"); Duplicate keeps the owner's references.
	Copy(ctx context.Context, in DuplicateInput) (Test, error)
	// CopyQuestions copies questions the actor can read into the bank of the
	// owner of a test the actor can edit, answering the copies in order.
	CopyQuestions(ctx context.Context, in QuestionCopies) ([]string, error)
	Delete(ctx context.Context, req Request, now time.Time) error
	DeleteVersion(ctx context.Context, req VersionRequest, now time.Time) error
	SetCurrentVersion(ctx context.Context, req VersionRequest, now time.Time) (Test, error)
	CreateDraftFromVersion(ctx context.Context, req VersionRequest, now time.Time) (Test, error)
	ListVersions(ctx context.Context, scope access.Scope, testID string) ([]Version, error)
	Preview(ctx context.Context, scope access.Scope, testID string, version int) (PreviewPaper, error)
	DiffPapers(ctx context.Context, req DiffRequest) (DiffPapers, error)
	GroupContexts(ctx context.Context, versionID string) ([]PreviewGroup, error)
	Publish(ctx context.Context, req PublishRequest, now time.Time, validate func(DraftContent) error) (Version, error)
	// EditQuestion edits a standalone question of the test's draft in the
	// test's context, forking it when the copy-on-write rule says so.
	EditQuestion(ctx context.Context, in QuestionEdit) (QuestionEdited, error)
}
