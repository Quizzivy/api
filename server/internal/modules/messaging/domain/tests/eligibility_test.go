package domain_test

import (
	"errors"
	"testing"

	"quizzivy/internal/modules/messaging/domain"
)

// student is an enabled student account with nothing tying it to the caller:
// another teacher's, or one in no class (F2).
var student = domain.DirectFacts{Exists: true, Enabled: true, Student: true}

func with(f domain.DirectFacts, change func(*domain.DirectFacts)) domain.DirectFacts {
	change(&f)
	return f
}

// TestTheEligibilityTableForADirectThread is F2's staff rule, "a thread, or a
// student, never yourself": no fact about classes, ties or who created the
// account takes part, so the facts a student of the caller's class and one
// in no class bring are the same.
func TestTheEligibilityTableForADirectThread(t *testing.T) {
	rules := domain.Eligibility{}
	for name, c := range map[string]struct {
		facts      domain.DirectFacts
		read, post error
	}{
		"any enabled student, taught by anyone or in no class": {student, nil, nil},
		"an enabled student with a thread":                     {with(student, func(f *domain.DirectFacts) { f.Conversation = true }), nil, nil},
		"a disabled student with a thread":                     {with(student, func(f *domain.DirectFacts) { f.Conversation, f.Enabled = true, false }), nil, domain.ErrNotAllowed},
		"a disabled student without a thread":                  {with(student, func(f *domain.DirectFacts) { f.Enabled = false }), domain.ErrNotFound, domain.ErrNotFound},
		"a staff account without a thread":                     {with(student, func(f *domain.DirectFacts) { f.Student = false }), domain.ErrNotFound, domain.ErrNotFound},
		"a thread whose student became staff":                  {with(student, func(f *domain.DirectFacts) { f.Conversation, f.Student = true, false }), nil, domain.ErrNotAllowed},
		"an anonymised account":                                {with(student, func(f *domain.DirectFacts) { f.Anonymised, f.Enabled = true, false }), domain.ErrNotFound, domain.ErrNotFound},
		"an anonymised account a thread still names":           {with(student, func(f *domain.DirectFacts) { f.Anonymised, f.Conversation = true, true }), domain.ErrNotFound, domain.ErrNotFound},
		"an id no account has":                                 {domain.DirectFacts{}, domain.ErrNotFound, domain.ErrNotFound},
		"the caller naming themselves":                         {with(student, func(f *domain.DirectFacts) { f.Self = true }), domain.ErrNotFound, domain.ErrNotFound},
	} {
		if err := rules.ReadDirect(c.facts); !errors.Is(err, c.read) {
			t.Errorf("%s: read = %v, want %v", name, err, c.read)
		}
		if err := rules.PostDirect(c.facts); !errors.Is(err, c.post) {
			t.Errorf("%s: post = %v, want %v", name, err, c.post)
		}
		if got := rules.CanPost(c.facts); got != (c.post == nil) {
			t.Errorf("%s: CanPost = %v, want %v", name, got, c.post == nil)
		}
		if got := rules.Peer(c.facts); got != (c.read == nil) {
			t.Errorf("%s: Peer = %v, want %v: a peer is exactly whom the thread is readable to", name, got, c.read == nil)
		}
	}
}

func TestTheEligibilityTableForAClassChannel(t *testing.T) {
	rules := domain.Eligibility{}
	for name, c := range map[string]struct {
		facts      domain.ChannelFacts
		read, post error
	}{
		"the class's teacher":                {domain.ChannelFacts{InScope: true, MayWrite: true}, nil, nil},
		"a reader who may not write classes": {domain.ChannelFacts{InScope: true}, nil, domain.ErrForbidden},
		"an archived class":                  {domain.ChannelFacts{InScope: true, MayWrite: true, Archived: true}, nil, domain.ErrClassArchived},
		"another teacher's class":            {domain.ChannelFacts{MayWrite: true}, domain.ErrNotFound, domain.ErrNotFound},
		"another teacher's archived class":   {domain.ChannelFacts{MayWrite: true, Archived: true}, domain.ErrNotFound, domain.ErrNotFound},
	} {
		if err := rules.ReadChannel(c.facts); !errors.Is(err, c.read) {
			t.Errorf("%s: read = %v, want %v", name, err, c.read)
		}
		if err := rules.PostChannel(c.facts); !errors.Is(err, c.post) {
			t.Errorf("%s: post = %v, want %v", name, err, c.post)
		}
	}
}

// pupil is a student naming an enabled staff account with nothing tying the
// two.
var pupil = domain.StudentFacts{Exists: true, TeacherEnabled: true, TeacherStaff: true, Student: true}

func as(f domain.StudentFacts, change func(*domain.StudentFacts)) domain.StudentFacts {
	change(&f)
	return f
}

// An archived class has no fact here: teachesNow leaves it out of Teaches in
// SQL, so its teacher arrives as a former teacher's facts.
// TestTheStudentEligibilityTableOnTheDatabase tests the archived rows.
func TestTheEligibilityTableForAStudentsDirectThread(t *testing.T) {
	rules := domain.Eligibility{}
	for name, c := range map[string]struct {
		facts      domain.StudentFacts
		read, post error
	}{
		"the teacher of a class the student is in":         {as(pupil, func(f *domain.StudentFacts) { f.Teaches = true }), nil, nil},
		"a thread the teacher started":                     {as(pupil, func(f *domain.StudentFacts) { f.Conversation = true }), nil, nil},
		"a former teacher with a thread":                   {as(pupil, func(f *domain.StudentFacts) { f.Conversation = true }), nil, nil},
		"a former teacher without a thread":                {pupil, domain.ErrNotFound, domain.ErrNotFound},
		"an id no account has":                             {domain.StudentFacts{Student: true}, domain.ErrNotFound, domain.ErrNotFound},
		"the student naming themselves":                    {as(pupil, func(f *domain.StudentFacts) { f.Self, f.Teaches = true, true }), domain.ErrNotFound, domain.ErrNotFound},
		"a disabled teacher of the student's class":        {as(pupil, func(f *domain.StudentFacts) { f.Teaches, f.TeacherEnabled = true, false }), nil, domain.ErrNotAllowed},
		"a thread whose teacher is no longer staff":        {as(pupil, func(f *domain.StudentFacts) { f.Conversation, f.TeacherStaff = true, false }), nil, domain.ErrNotAllowed},
		"a thread kept by a caller no longer a student":    {as(pupil, func(f *domain.StudentFacts) { f.Conversation, f.Student = true, false }), nil, domain.ErrNotAllowed},
		"a staff caller sitting in the teacher's class":    {as(pupil, func(f *domain.StudentFacts) { f.Teaches, f.Student = true, false }), domain.ErrNotFound, domain.ErrNotFound},
		"a disabled teacher the student has no class with": {as(pupil, func(f *domain.StudentFacts) { f.TeacherEnabled = false }), domain.ErrNotFound, domain.ErrNotFound},
	} {
		if err := rules.ReadAsStudent(c.facts); !errors.Is(err, c.read) {
			t.Errorf("%s: read = %v, want %v", name, err, c.read)
		}
		if err := rules.PostAsStudent(c.facts); !errors.Is(err, c.post) {
			t.Errorf("%s: post = %v, want %v", name, err, c.post)
		}
		if got := rules.CanPostAsStudent(c.facts); got != (c.post == nil) {
			t.Errorf("%s: CanPostAsStudent = %v, want %v", name, got, c.post == nil)
		}
		if got := rules.Teacher(c.facts); got != (c.read == nil) {
			t.Errorf("%s: Teacher = %v, want %v: whom a student may name is exactly whom the thread is readable to", name, got, c.read == nil)
		}
	}
}
