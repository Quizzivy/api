package domain

import (
	"quizzivy/internal/shared/access"
	"quizzivy/internal/shared/validation"
	"strconv"
	"strings"
	"time"
)

// ListInput selects a page of tests.
type ListInput struct {
	Status *Status
	// Tags filter by the tags of the questions a test CONTAINS.
	Tags  []string
	Query string
	Page  int
	Limit int
	Scope access.Scope
}

// CreateInput is a new empty draft, owned by OwnerID when set and by ActorID
// otherwise; ActorID is recorded as its creator either way.
type CreateInput struct {
	Title       string
	Description *string
	ActorID     string
	OwnerID     string
	Now         time.Time
	IP          string
	UserAgent   string
}

// UpdateRequest is one autosave.
type UpdateRequest struct {
	ID        string
	Input     UpdateInput
	ActorID   string
	Now       time.Time
	IP        string
	UserAgent string
	Scope     access.Scope
}

// UpdateInput is the autosave body. Nil fields are left alone; a non-nil
// Sections replaces the whole outline.
type UpdateInput struct {
	ExpectedUpdatedAt time.Time
	Title             *string
	Description       *string
	SetDescription    bool
	Status            *Status
	Sections          []SectionInput
	SetSections       bool
	GroupOutline      bool
}

// SectionInput is one section of a whole-outline write. An empty ID creates.
type SectionInput struct {
	ID           string
	Title        string
	Instructions *string
	QuestionIDs  []string
	Units        []SectionUnit
	SetUnits     bool
}

// DuplicateInput copies a test's draft structure.
type DuplicateInput struct {
	ID        string
	ActorID   string
	Now       time.Time
	IP        string
	UserAgent string
	Scope     access.Scope
}

// Request identifies a test and who acts on it; Scope is whose tests the
// actor may reach, and a test outside it is ErrNotFound.
type Request struct {
	ID        string
	ActorID   string
	IP        string
	UserAgent string
	Scope     access.Scope
}

func sectionField(i int, field string) string {
	return "sections[" + strconv.Itoa(i) + "]." + field
}

// Validate checks the parts of an outline write a schema cannot express.
func (in UpdateInput) Validate() error {
	errs := &ValidationError{}

	if in.Title != nil && strings.TrimSpace(*in.Title) == "" {
		errs.AddIn("title", "Tên đề không được để trống.", "The test name cannot be empty.")
	}
	if in.Status != nil && !in.Status.valid() {
		errs.AddIn("status", "Trạng thái không hợp lệ.", "The status is not valid.")
	}
	if in.Status != nil && *in.Status == Published {
		errs.AddIn("status", "Dùng thao tác xuất bản để chuyển đề sang trạng thái published.", "Use the publish operation to move the test to published.")
	}

	for i, s := range in.Sections {
		if strings.TrimSpace(s.Title) == "" {
			errs.AddIn(sectionField(i, "title"), "Tên phần không được để trống.", "The section name cannot be empty.")
		}
		seen := make(map[string]bool, len(s.QuestionIDs))
		for _, id := range s.QuestionIDs {
			if seen[id] {
				errs.AddIn(sectionField(i, "questionIds"), "Một câu hỏi chỉ được xuất hiện một lần trong phần.", "A question can appear only once in a section.")
				break
			}
			seen[id] = true
		}
	}
	validateMixedOutline(in, errs)

	if err := errs.OrNil(); err != nil {
		return err
	}
	return nil
}

type (
	FieldError      = validation.Field
	ValidationError = validation.Error
)

// VersionRequest identifies a snapshot and the test revision observed by the teacher.
type VersionRequest struct {
	Request
	Version           int
	ExpectedUpdatedAt time.Time
}
