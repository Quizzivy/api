package command

import (
	"context"
	"errors"
	"fmt"
	"quizzivy/internal/modules/identity/application/internal/support"
	"quizzivy/internal/modules/identity/application/model"
	"quizzivy/internal/modules/identity/domain"
	"quizzivy/internal/shared/access"
)

// GoogleSignIn implements §5.3 in full.
type GoogleSignIn struct {
	Code         string
	CodeVerifier string
	RedirectURI  string
	JoinCode     string
	Audience     access.Audience
	UserAgent    string
	IP           string
	GeoLabel     string
}

type GoogleSignInHandler struct {
	*support.Service
}

// Handle fails with domain.ErrWrongHost for an account that does not belong at
// cmd.Audience, and, before Google is asked anything, for a join code at the
// staff host: a code makes a student.
func (s GoogleSignInHandler) Handle(ctx context.Context, cmd GoogleSignIn) (model.GoogleSignInResult, error) {
	if s.Google == nil {
		return model.GoogleSignInResult{}, domain.ErrGoogleUnavailable
	}
	if cmd.JoinCode != "" && cmd.Audience == access.AudienceStaff {
		return model.GoogleSignInResult{}, domain.ErrWrongHost
	}
	identity, err := s.VerifiedIdentity(ctx, cmd.Code, cmd.CodeVerifier, cmd.RedirectURI)
	if err != nil {
		return model.GoogleSignInResult{}, err
	}

	user, err := s.Users.FindUserByProviderIdentity(ctx, "google", identity.Subject)
	switch {
	case err == nil:
		return s.GoogleSession(ctx, user, support.GoogleSignInInput(cmd), nil)
	case !errors.Is(err, domain.ErrUserNotFound):
		return model.GoogleSignInResult{}, fmt.Errorf("look up google identity: %w", err)
	}

	user, err = s.Users.FindUserByEmail(ctx, identity.Email)
	switch {
	case err == nil:
		linked, err := s.LinkAndReload(ctx, user.ID, identity)
		if err != nil {
			return model.GoogleSignInResult{}, err
		}
		return s.GoogleSession(ctx, linked, support.GoogleSignInInput(cmd), nil)
	case !errors.Is(err, domain.ErrUserNotFound):
		return model.GoogleSignInResult{}, fmt.Errorf("look up user by email: %w", err)
	}

	if cmd.JoinCode != "" {
		return s.EnrolByCode(ctx, identity, support.GoogleSignInInput(cmd))
	}

	return model.GoogleSignInResult{}, domain.ErrAccountNotProvisioned
}
