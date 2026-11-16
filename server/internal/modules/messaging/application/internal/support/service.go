package support

import (
	"context"

	"quizzivy/internal/modules/messaging/application/ports"
	"quizzivy/internal/modules/messaging/domain"
)

// Service carries what the handlers share: the conversations, the rules that
// decide who reads and posts, and the photo signer.
type Service struct {
	Repo    domain.Repository
	Rules   domain.Eligibility
	Avatars ports.Avatars
}

// Sign returns the signed URL of the photo key names, once per key in signed,
// or nil when there is none to show or no signer.
func (s *Service) Sign(ctx context.Context, key *string, signed map[string]*string) *string {
	if key == nil || *key == "" || s.Avatars == nil {
		return nil
	}
	if url, seen := signed[*key]; seen {
		return url
	}
	var out *string
	if url, err := s.Avatars.AvatarURL(ctx, *key); err == nil && url != "" {
		out = &url
	}
	signed[*key] = out
	return out
}

// SignMessages fills each message's sender photo, and Mine for reader.
func (s *Service) SignMessages(ctx context.Context, readerID string, messages []domain.Message, signed map[string]*string) {
	for i := range messages {
		s.SignMessage(ctx, readerID, &messages[i], signed)
	}
}

// SignMessage fills m's sender photo, and Mine for reader.
func (s *Service) SignMessage(ctx context.Context, readerID string, m *domain.Message, signed map[string]*string) {
	m.SenderAvatarURL = s.Sign(ctx, m.SenderAvatarKey, signed)
	m.Mine = m.SenderID == readerID
}

// Direct resolves studentID as a messaging peer of staffID, by the rules'
// ReadDirect: the facts, or domain.ErrNotFound.
func (s *Service) Direct(ctx context.Context, staffID, studentID string) (domain.DirectFacts, error) {
	facts, err := s.Repo.DirectFacts(ctx, staffID, studentID)
	if err != nil {
		return domain.DirectFacts{}, err
	}
	return facts, s.Rules.ReadDirect(facts)
}
