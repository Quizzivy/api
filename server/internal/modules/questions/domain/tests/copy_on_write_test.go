package domain_test

import (
	"testing"

	"quizzivy/internal/modules/questions/domain"
)

// TestTheCopyOnWriteRule is QuestionUse.Forks case by case (D4, T-R7.6).
func TestTheCopyOnWriteRule(t *testing.T) {
	const owner, editor = "owner", "editor"
	inTest := domain.EditContext{Kind: domain.ContextTest, ID: "t1"}
	inSet := domain.EditContext{Kind: domain.ContextSet, ID: "s2"}
	cases := []struct {
		name    string
		actor   string
		context domain.EditContext
		use     domain.QuestionUse
		forks   bool
	}{
		{"a non-owner in a test, another draft uses it", editor, inTest, domain.QuestionUse{OwnerID: owner, OtherDrafts: 1}, true},
		{"a non-owner in a test, a set uses it", editor, inTest, domain.QuestionUse{OwnerID: owner, OtherSets: 1}, true},
		{"a non-owner in a set, another set uses it", editor, inSet, domain.QuestionUse{OwnerID: owner, OtherSets: 2}, true},
		{"a non-owner in a set, a draft uses it", editor, inSet, domain.QuestionUse{OwnerID: owner, OtherDrafts: 1}, true},
		{"a non-owner, nothing else uses it", editor, inTest, domain.QuestionUse{OwnerID: owner}, false},
		{"the owner, whatever else uses it", owner, inTest, domain.QuestionUse{OwnerID: owner, OtherDrafts: 3, OtherSets: 3}, false},
		{"no context", editor, domain.EditContext{}, domain.QuestionUse{OwnerID: owner, OtherDrafts: 1}, false},
		{"a context with no id", editor, domain.EditContext{Kind: domain.ContextTest}, domain.QuestionUse{OwnerID: owner, OtherDrafts: 1}, false},
		{"a group member", editor, inTest, domain.QuestionUse{OwnerID: owner, GroupMember: true, OtherDrafts: 1}, false},
	}
	for _, c := range cases {
		if got := c.use.Forks(c.actor, c.context); got != c.forks {
			t.Errorf("%s: forks %v, want %v", c.name, got, c.forks)
		}
	}
}

func TestAnEditKeepsOnlyTheMediaTheQuestionBinds(t *testing.T) {
	use := domain.QuestionUse{Media: []string{"01935000-0000-7000-8000-00000000000a"}}
	if !use.Keeps("01935000-0000-7000-8000-00000000000A") {
		t.Error("the question's own asset, in upper case, is not kept")
	}
	if use.Keeps("01935000-0000-7000-8000-00000000000b") {
		t.Error("an asset the question does not bind is kept")
	}
}
