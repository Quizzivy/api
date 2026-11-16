package domain_test

import (
	"errors"
	"testing"

	"quizzivy/internal/modules/messaging/domain"
)

// student is an enabled student account with nothing tying it to the caller.
var student = domain.DirectFacts{Exists: true, Enabled: true, Student: true}

func with(f domain.DirectFacts, change func(*domain.DirectFacts)) domain.DirectFacts {
	change(&f)
	return f
}

func TestTheEligibilityTableForADirectThread(t *testing.T) {
	rules := domain.Eligibility{}
	for name, c := range map[string]struct {
		facts      domain.DirectFacts
		read, post error
	}{
		"a student of the caller's class":           {with(student, func(f *domain.DirectFacts) { f.Tied = true }), nil, nil},
		"a student the caller created":              {with(student, func(f *domain.DirectFacts) { f.Created = true }), nil, nil},
		"a removed student, through the tie":        {with(student, func(f *domain.DirectFacts) { f.Tied = true }), nil, nil},
		"a removed student with a thread":           {with(student, func(f *domain.DirectFacts) { f.Tied, f.Conversation = true, true }), nil, nil},
		"a thread outliving every tie":              {with(student, func(f *domain.DirectFacts) { f.Conversation = true }), nil, nil},
		"another teacher's student":                 {student, domain.ErrNotFound, domain.ErrNotFound},
		"another teacher's disabled student":        {with(student, func(f *domain.DirectFacts) { f.Enabled = false }), domain.ErrNotFound, domain.ErrNotFound},
		"an id no account has":                      {domain.DirectFacts{}, domain.ErrNotFound, domain.ErrNotFound},
		"the caller naming themselves":              {with(student, func(f *domain.DirectFacts) { f.Self, f.Created = true, true }), domain.ErrNotFound, domain.ErrNotFound},
		"a tied student, disabled":                  {with(student, func(f *domain.DirectFacts) { f.Tied, f.Enabled = true, false }), nil, domain.ErrNotAllowed},
		"a tied account, no longer a student":       {with(student, func(f *domain.DirectFacts) { f.Tied, f.Student = true, false }), nil, domain.ErrNotAllowed},
		"a thread whose student was disabled":       {with(student, func(f *domain.DirectFacts) { f.Conversation, f.Enabled = true, false }), nil, domain.ErrNotAllowed},
		"a staff account the caller created":        {with(student, func(f *domain.DirectFacts) { f.Created, f.Student = true, false }), domain.ErrNotFound, domain.ErrNotFound},
		"a student the caller created, disabled":    {with(student, func(f *domain.DirectFacts) { f.Created, f.Enabled = true, false }), nil, domain.ErrNotAllowed},
		"a created account promoted after a thread": {with(student, func(f *domain.DirectFacts) { f.Created, f.Conversation, f.Student = true, true, false }), nil, domain.ErrNotAllowed},
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
		"an archived class's teacher, without a thread":    {pupil, domain.ErrNotFound, domain.ErrNotFound}, // an archived class is no Teaches
		"an archived class's teacher, with a thread":       {as(pupil, func(f *domain.StudentFacts) { f.Conversation = true }), nil, nil},
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
