package application_test

import (
	"context"
	"errors"
	"quizzivy/internal/modules/identity/application"
	"quizzivy/internal/modules/identity/application/command"
	"quizzivy/internal/modules/identity/domain"
	"strings"
	"testing"
	"time"
)

// F1: assignmentDefaults.mode is stored as either mode, and nothing else.
func TestTheDefaultAssignmentModeIsStrictOrFlexible(t *testing.T) {
	for _, mode := range []string{"strict", "flexible"} {
		repo := &profileUsers{}
		app := application.New(repo, nil, time.Hour, nil, nil)
		p := domain.Preferences{AssignmentDefaults: &domain.AssignmentDefaults{Mode: profileName(mode)}}
		if _, err := app.Commands.UpdatePreferences.Handle(context.Background(), command.UpdatePreferences{UserID: "a", Preferences: p}); err != nil {
			t.Fatalf("%s: %v", mode, err)
		}
		if !strings.Contains(string(repo.prefs.Patch), `"mode":"`+mode+`"`) {
			t.Errorf("%s: the patch %s does not carry the mode", mode, repo.prefs.Patch)
		}
	}
	for _, mode := range []string{"", "relaxed", "Strict"} {
		repo := &profileUsers{}
		app := application.New(repo, nil, time.Hour, nil, nil)
		p := domain.Preferences{AssignmentDefaults: &domain.AssignmentDefaults{Mode: profileName(mode)}}
		if _, err := app.Commands.UpdatePreferences.Handle(context.Background(), command.UpdatePreferences{UserID: "a", Preferences: p}); !errors.Is(err, domain.ErrPreferencesInvalid) || repo.calls != 0 {
			t.Errorf("mode %q: error=%v calls=%d", mode, err, repo.calls)
		}
	}
}
