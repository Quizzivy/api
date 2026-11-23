package retention_test

import (
	"testing"

	"quizzivy/internal/shared/retention"
)

// TestNotAnonymousOfIsNotAnonymousOverAnotherAlias: the alias form is the
// same rule, so a subquery over another alias (visibility.MessagingPeerIDs)
// reads an anonymised account as anonymisation writes it.
func TestNotAnonymousOfIsNotAnonymousOverAnotherAlias(t *testing.T) {
	if got := retention.NotAnonymousOf("u"); got != retention.NotAnonymous {
		t.Errorf("NotAnonymousOf(u) = %q, want NotAnonymous %q", got, retention.NotAnonymous)
	}
	if got, want := retention.NotAnonymousOf("w"), `w.email <> 'anonymous-' || w.id::text || '@anonymous.invalid'`; got != want {
		t.Errorf("NotAnonymousOf(w) = %q, want %q", got, want)
	}
}
