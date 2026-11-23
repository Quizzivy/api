package support

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"quizzivy/internal/modules/organization/application/ports"
	"quizzivy/internal/modules/organization/domain"
)

// Service carries what the handlers share: the store, the snapshot, the logo
// store and checker (nil without object storage), the mail dispatcher to wake
// when the usage limits change (nil when nothing sends mail), the terms store
// (nil in a process that serves no terms), the profile zones (nil reads every
// account as the centre's zone), the clock and whether this server signs
// anyone in with Google.
type Service struct {
	Repo             domain.Repository
	Snapshot         *Snapshot
	Logos            ports.LogoStore
	Checker          ports.LogoChecker
	Mail             ports.MailWake
	Terms            domain.Terms
	Zones            ports.Zones
	Now              func() time.Time
	GoogleConfigured bool

	mu    sync.Mutex
	bytes map[string][]byte
}

// LogoPrefix is the start of every key a logo is stored under.
const LogoPrefix = "branding/"

// LogoKey is where a logo's bytes are stored.
func LogoKey(logo domain.Logo) string {
	return LogoPrefix + hex.EncodeToString(logo.SHA256) + "." + domain.LogoExtension
}

// LogoBytes returns logo's bytes, from memory after the first read: a logo's
// bytes never change under its key. Only the logos in use are remembered.
func (s *Service) LogoBytes(ctx context.Context, logo domain.Logo, inUse ...*domain.Logo) ([]byte, error) {
	if s.Logos == nil {
		return nil, domain.ErrStorageOff
	}
	key := LogoKey(logo)
	s.mu.Lock()
	cached, ok := s.bytes[key]
	s.mu.Unlock()
	if ok {
		return cached, nil
	}
	data, err := s.Logos.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	keep := map[string][]byte{key: data}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, other := range inUse {
		if other == nil {
			continue
		}
		if b, ok := s.bytes[LogoKey(*other)]; ok {
			keep[LogoKey(*other)] = b
		}
	}
	s.bytes = keep
	return data, nil
}

// ErrNoTerms is a terms operation on a process built without the terms store.
var ErrNoTerms = errors.New("organization: this process serves no terms")

// TermStore is the terms store, or ErrNoTerms.
func (s *Service) TermStore() (domain.Terms, error) {
	if s.Terms == nil {
		return nil, ErrNoTerms
	}
	return s.Terms, nil
}

// TodayOf is the calendar day it is now for userID: in the zone their profile
// names, else domain.DefaultZone.
func (s *Service) TodayOf(ctx context.Context, userID string) (time.Time, error) {
	zone := ""
	if s.Zones != nil && userID != "" {
		stored, err := s.Zones.ZoneOf(ctx, userID)
		if err != nil {
			return time.Time{}, fmt.Errorf("organization: the viewer's zone: %w", err)
		}
		zone = stored
	}
	return domain.Today(s.Now(), domain.LocationOf(zone)), nil
}
