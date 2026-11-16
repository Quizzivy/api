package support

import (
	"context"
	"quizzivy/internal/modules/questions/application/ports"
	"quizzivy/internal/modules/questions/domain"
	"quizzivy/internal/shared/access"
	"time"
)

// Service carries what the service handlers share: their ports and the helpers they call.
type Service struct {
	Repo  domain.Repository
	Media ports.MediaKinds
	// Sets and Contexts are nil until the application is given them (WithSets),
	// and Tests until it is given the tests module's edit rule (WithTests).
	Sets     domain.SetRepository
	Contexts domain.WriteContexts
	Tests    ports.EditableTests
	Now      func() time.Time
}

func NewService(repo domain.Repository, media ports.MediaKinds) *Service {
	return &Service{Repo: repo, Media: media, Now: time.Now}
}

func (s *Service) ResolveMediaKind(ctx context.Context, scope access.Scope, assetID *string) (*string, error) {
	if assetID == nil {
		return nil, nil
	}
	if s.Media == nil {
		return nil, domain.ErrMediaNotFound
	}
	kind, err := s.Media.Kind(ctx, scope, *assetID)
	if err != nil {
		return nil, err
	}
	return &kind, nil
}

func (s *Service) Write(ctx context.Context, req domain.WriteRequest, update bool) (domain.Question, error) {
	kind, err := s.ResolveMediaKind(ctx, req.MediaScope(), req.Input.MediaAssetID)
	if err != nil {
		return domain.Question{}, err
	}
	if err := req.Input.Validate(kind); err != nil {
		return domain.Question{}, err
	}

	in := domain.WriteInput{
		ID:             req.ID,
		Input:          req.Input,
		MediaAssetKind: kind,
		ActorID:        req.ActorID,
		OwnerID:        req.OwnerID,
		All:            req.All,
		SourceMedia:    req.SourceMedia,
		Now:            s.Now(),
		IP:             req.IP,
		UserAgent:      req.UserAgent,
	}
	write := s.Repo.Create
	if update {
		write = s.Repo.Update
	}
	written, err := write(ctx, in)
	if err != nil {
		return domain.Question{}, err
	}
	written.Access = domain.WriterAccess(req)
	return written, nil
}
