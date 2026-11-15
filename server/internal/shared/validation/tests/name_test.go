package validation_test

import (
	"strings"
	"testing"

	"quizzivy/internal/shared/validation"
)

// TestCleanNameMeasuresANameAgainstItsCallersLimit fails when CleanName
// measures every name against one fixed length, or measures it before
// tidying: an account's name may be longer than the name the consultation form
// takes. The identity tests cover the rule itself through CleanFullName, at
// that module's one limit.
func TestCleanNameMeasuresANameAgainstItsCallersLimit(t *testing.T) {
	name := strings.Repeat("ê", 121)
	if _, fault, _ := validation.CleanName(name, 120); fault != validation.NameTooLong {
		t.Errorf("121 characters at a limit of 120: fault %v, want too long", fault)
	}
	if got, fault, _ := validation.CleanName(name, 200); fault != validation.NameOK || got != name {
		t.Errorf("121 characters at a limit of 200: fault %v, want kept", fault)
	}
	padded := "  " + strings.Repeat("ê", 60) + " \t " + strings.Repeat("ê", 59) + "  "
	if got, fault, _ := validation.CleanName(padded, 120); fault != validation.NameOK || len([]rune(got)) != 120 {
		t.Errorf("a name of 120 characters once tidied: fault %v, %d characters", fault, len([]rune(got)))
	}
}
