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
		"a student of the caller's class":     {with(student, func(f *domain.DirectFacts) { f.Tied = true }), nil, nil},
		"a student the caller created":        {with(student, func(f *domain.DirectFacts) { f.Created = true }), nil, nil},
		"a removed student, through the tie":  {with(student, func(f *domain.DirectFacts) { f.Tied = true }), nil, nil},
		"a removed student with a thread":     {with(student, func(f *domain.DirectFacts) { f.Tied, f.Conversation = true, true }), nil, nil},
		"a thread outliving every tie":        {with(student, func(f *domain.DirectFacts) { f.Conversation = true }), nil, nil},
		"another teacher's student":           {student, domain.ErrNotFound, domain.ErrNotFound},
		"another teacher's disabled student":  {with(student, func(f *domain.DirectFacts) { f.Enabled = false }), domain.ErrNotFound, domain.ErrNotFound},
		"an id no account has":                {domain.DirectFacts{}, domain.ErrNotFound, domain.ErrNotFound},
		"the caller naming themselves":        {with(student, func(f *domain.DirectFacts) { f.Self, f.Created = true, true }), domain.ErrNotFound, domain.ErrNotFound},
		"a tied student, disabled":            {with(student, func(f *domain.DirectFacts) { f.Tied, f.Enabled = true, false }), nil, domain.ErrNotAllowed},
		"a tied account, no longer a student": {with(student, func(f *domain.DirectFacts) { f.Tied, f.Student = true, false }), nil, domain.ErrNotAllowed},
		"a thread whose student was disabled": {with(student, func(f *domain.DirectFacts) { f.Conversation, f.Enabled = true, false }), nil, domain.ErrNotAllowed},
		"a staff account the caller created":  {with(student, func(f *domain.DirectFacts) { f.Created, f.Student = true, false }), nil, domain.ErrNotAllowed},
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
