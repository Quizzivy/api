package command

import (
	"context"
	"crypto/sha256"
	"fmt"
	"quizzivy/internal/modules/identity/application/internal/support"
	"quizzivy/internal/modules/identity/application/model"
	"quizzivy/internal/modules/identity/domain"
	"quizzivy/internal/shared/access"
	"quizzivy/internal/shared/opt"
)

// Refresh rotates a refresh token (§5.2). The rotation runs on the request's own
// context, deliberately; docs/plan/30-risks.md R-06 says why.
type Refresh struct {
	Token     string
	Audience  access.Audience
	UserAgent string
	IP        string
	GeoLabel  string
}

type RefreshHandler struct {
	*support.Service
}

// Handle rotates the token of a session of cmd.Audience. A session whose
// account no longer belongs at that host is revoked and refused.
func (s RefreshHandler) Handle(ctx context.Context, cmd Refresh) (model.RefreshResult, error) {
	if cmd.Token == "" {
		return model.RefreshResult{}, domain.ErrRefreshRejected
	}
	presented := sha256.Sum256([]byte(cmd.Token))

	successor, successorHash, err := support.NewRefreshToken()
	if err != nil {
		return model.RefreshResult{}, err
	}

	now := s.Now()
	next := domain.RefreshTokenRecord{
		Audience:  cmd.Audience,
		TokenHash: successorHash,
		IssuedAt:  now,
		ExpiresAt: now.Add(s.RefreshTTL),
	}
	next.UserAgent = opt.String(cmd.UserAgent)
	next.IP = opt.String(cmd.IP)
	next.GeoLabel = opt.String(cmd.GeoLabel)

	res, err := s.Users.Rotate(ctx, presented[:], next, now)
	if err != nil {
		return model.RefreshResult{}, fmt.Errorf("rotate refresh token: %w", err)
	}
	switch res.Outcome {
	case domain.RotateOK:
	case domain.RotateReused:

		return model.RefreshResult{}, domain.ErrRefreshReused
	default:
		return model.RefreshResult{}, domain.ErrRefreshRejected
	}

	principal, err := s.Principals.Resolve(ctx, res.User.ID)
	if err != nil {
		return model.RefreshResult{}, fmt.Errorf("resolve permissions: %w", err)
	}
	if !cmd.Audience.Admits(principal.Permissions) {
		if err := s.Users.RevokeFamily(ctx, res.User.ID, res.FamilyID, now); err != nil {
			return model.RefreshResult{}, fmt.Errorf("end a session at the wrong host: %w", err)
		}
		return model.RefreshResult{}, domain.ErrRefreshRejected
	}

	access, err := s.Tokens.IssueFor(res.User.ID, res.User.SessionEpoch, cmd.Audience)
	if err != nil {
		return model.RefreshResult{}, fmt.Errorf("issue access token: %w", err)
	}

	return model.RefreshResult{
		AccessToken:  access,
		ExpiresIn:    int(s.Tokens.TTL().Seconds()),
		RefreshToken: successor,
		User:         res.User,
	}, nil
}
