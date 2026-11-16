package domain_test

import (
	"errors"
	"testing"

	"quizzivy/internal/modules/sharing/domain"
	"quizzivy/internal/shared/access"
)

const (
	owner     = "01935000-0000-7000-8000-0000000000a1"
	admin     = "01935000-0000-7000-8000-0000000000a2"
	colleague = "01935000-0000-7000-8000-0000000000a3"
)

func TestEachRecipientIsSkippedForTheFirstReasonThatApplies(t *testing.T) {
	teacher := domain.Candidate{UserID: colleague, Exists: true, Teacher: true}
	cases := map[string]struct {
		candidate domain.Candidate
		actor     string
		code      domain.SkipCode
		skipped   bool
	}{
		"a colleague":                     {teacher, owner, "", false},
		"the owner":                       {domain.Candidate{UserID: owner, Exists: true, Teacher: true}, owner, domain.SkipSelf, true},
		"the owner, named by an Admin":    {domain.Candidate{UserID: owner, Exists: true, Teacher: true}, admin, domain.SkipSelf, true},
		"the Admin sharing":               {domain.Candidate{UserID: admin, Exists: true, Teacher: true}, admin, domain.SkipSelf, true},
		"no account":                      {domain.Candidate{UserID: colleague}, owner, domain.SkipNotATeacher, true},
		"a student":                       {domain.Candidate{UserID: colleague, Exists: true}, owner, domain.SkipNotATeacher, true},
		"a disabled student":              {domain.Candidate{UserID: colleague, Exists: true, Disabled: true}, owner, domain.SkipNotATeacher, true},
		"a holder of a grant":             {domain.Candidate{UserID: colleague, Exists: true, Teacher: true, Granted: true}, owner, domain.SkipAlreadyShared, true},
		"a disabled holder of a grant":    {domain.Candidate{UserID: colleague, Exists: true, Teacher: true, Granted: true, Disabled: true}, owner, domain.SkipAlreadyShared, true},
		"a disabled colleague":            {domain.Candidate{UserID: colleague, Exists: true, Teacher: true, Disabled: true}, owner, domain.SkipDisabled, true},
		"a colleague the Admin names":     {teacher, admin, "", false},
		"a student who holds a stray row": {domain.Candidate{UserID: colleague, Exists: true, Granted: true}, owner, domain.SkipNotATeacher, true},
	}
	for name, c := range cases {
		code, skipped := domain.Skip(c.candidate, owner, c.actor)
		if code != c.code || skipped != c.skipped {
			t.Errorf("%s: Skip = %q, %v; want %q, %v", name, code, skipped, c.code, c.skipped)
		}
	}
}

func TestAnAccessChangeIsWrittenOnlyWhenTheLevelMoves(t *testing.T) {
	cases := []struct {
		from, to domain.Access
		changed  bool
		err      error
	}{
		{domain.AccessUse, domain.AccessEdit, true, nil},
		{domain.AccessEdit, domain.AccessUse, true, nil},
		{domain.AccessUse, domain.AccessUse, false, nil},
		{domain.AccessEdit, domain.AccessEdit, false, nil},
		{domain.AccessUse, "owner", false, domain.ErrInvalid},
		{domain.AccessEdit, "", false, domain.ErrInvalid},
	}
	for _, c := range cases {
		changed, err := domain.Transition(c.from, c.to)
		if changed != c.changed || !errors.Is(err, c.err) {
			t.Errorf("Transition(%q, %q) = %v, %v; want %v, %v", c.from, c.to, changed, err, c.changed, c.err)
		}
	}
}

func TestOnlyTheOwnerAndScopeAllManageAShare(t *testing.T) {
	res := domain.Resource{Kind: domain.KindTest, ID: "01935000-0000-7000-8000-0000000000b1", OwnerID: owner}
	cases := map[string]struct {
		scope    access.Scope
		readable bool
		holds    bool
		standing domain.Standing
		manage   error
	}{
		"the owner":                       {access.Scope{UserID: owner}, true, false, domain.Owner, nil},
		"scope.all":                       {access.Scope{UserID: admin, All: true}, true, false, domain.Owner, nil},
		"a grantee its module lets read":  {access.Scope{UserID: colleague}, true, false, domain.Grantee, domain.ErrOwnerOnly},
		"a grantee its module refuses":    {access.Scope{UserID: colleague}, false, true, domain.Grantee, domain.ErrOwnerOnly},
		"a teacher with no grant":         {access.Scope{UserID: colleague}, false, false, domain.Stranger, domain.ErrNotFound},
		"the owner of a refused resource": {access.Scope{UserID: owner}, false, false, domain.Stranger, domain.ErrNotFound},
	}
	for name, c := range cases {
		standing := domain.StandingOf(c.scope, res, c.readable, c.holds)
		if standing != c.standing || !errors.Is(standing.Manage(), c.manage) {
			t.Errorf("%s: standing %v manages with %v; want %v and %v", name, standing, standing.Manage(), c.standing, c.manage)
		}
	}
}

func TestTheSharersMessageIsTrimmedAndAnEmptyOneIsNone(t *testing.T) {
	blank, spaced, decomposed := "   ", "  Dùng cho lớp tối  ", "Cho lóp"
	if domain.CleanMessage(nil) != nil || domain.CleanMessage(&blank) != nil {
		t.Error("no message, or a blank one, is a message")
	}
	if got := domain.CleanMessage(&spaced); got == nil || *got != "Dùng cho lớp tối" {
		t.Errorf("trimmed message %v", got)
	}
	if got := domain.CleanMessage(&decomposed); got == nil || *got != "Cho lóp" {
		t.Errorf("the message is not composed to NFC: %q", *got)
	}
}

func TestOnlyTheContractsKindsAndLevelsAreKnown(t *testing.T) {
	for _, k := range []domain.Kind{domain.KindTest, domain.KindQuestionSet} {
		if !k.Valid() {
			t.Errorf("%q is not a kind", k)
		}
	}
	for _, k := range []domain.Kind{"", "question-set", "word_list"} {
		if k.Valid() {
			t.Errorf("%q is a kind", k)
		}
	}
	if !domain.AccessUse.Valid() || !domain.AccessEdit.Valid() || domain.Access("owner").Valid() {
		t.Error("the levels are use and edit only")
	}
}
