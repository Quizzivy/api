package support

import (
	"context"
	"log/slog"
	"time"

	"quizzivy/internal/modules/messaging/application/ports"
	"quizzivy/internal/modules/messaging/domain"
	"quizzivy/internal/shared/cqrs"
)

// Service carries what the handlers share: the conversations, the rules that
// decide who reads and posts, the photo signer, the flagged attempts a staff
// member's direct thread shows, the store and probe of messages' files, and
// the bell that holds the readers' in-app items, with the logger its
// failures go to.
type Service struct {
	Repo    domain.Repository
	Rules   domain.Eligibility
	Avatars ports.Avatars
	Events  ports.ContextEvents
	Files   ports.AttachmentStore
	Audio   ports.AudioProbe
	Bell    ports.Bell
	Logger  *slog.Logger
	Now     func() time.Time
}

// Ring tells the readers of a message just posted, once it has committed,
// through the bell: one Notice each (domain.Delivery). A replayed post tells
// nobody again. Telling never fails the post: a failure is logged.
//
// A reader whose pointer moves past the message after Delivery left them in
// but before their Notice is written has the item cleared by their own Seen
// too early, against a row that is not written yet or is older. So once every
// Notice is written, Ring reads again which of the readers have caught up
// (domain.Repository.CaughtUp) and clears their items as Seen would.
func (s *Service) Ring(ctx context.Context, posted domain.Posted) {
	if s.Bell == nil || !posted.Created {
		return
	}
	var delivery domain.Delivery
	ring := cqrs.HandlerFunc[domain.Notice, cqrs.Nothing](func(ctx context.Context, n domain.Notice) (cqrs.Nothing, error) {
		return cqrs.Nothing{}, s.Bell.Ring(ctx, n)
	})
	cqrs.Announce(ctx, ring, s.Logger, "message posted", func(ctx context.Context) ([]domain.Notice, error) {
		var err error
		if delivery, err = s.Repo.Delivery(ctx, posted.Message.ID); err != nil {
			return nil, err
		}
		return delivery.Notices(), nil
	})
	if len(delivery.Readers) == 0 {
		return
	}
	seen := cqrs.HandlerFunc[domain.Caught, cqrs.Nothing](func(ctx context.Context, c domain.Caught) (cqrs.Nothing, error) {
		return cqrs.Nothing{}, s.Bell.Seen(ctx, c.ReaderID, c.State.ConversationID, c.State.At)
	})
	cqrs.Announce(ctx, seen, s.Logger, "message read while rung", func(ctx context.Context) ([]domain.Caught, error) {
		return s.Repo.CaughtUp(ctx, delivery.ConversationID, delivery.Readers)
	})
}

// MoveRead runs move, which moves readerID's read pointer, and then Seen with
// the thread's state; move's error is returned as it is.
func (s *Service) MoveRead(ctx context.Context, readerID string, move func() (domain.ReadState, error)) error {
	state, err := move()
	if err != nil {
		return err
	}
	s.Seen(ctx, readerID, state)
	return nil
}

// Seen clears readerID's in-app item for the thread state names once their
// pointer has left nothing unread there, keeping one a message written after
// the state was judged left unread. Clearing never fails the read: a failure
// is logged.
func (s *Service) Seen(ctx context.Context, readerID string, state domain.ReadState) {
	if s.Bell == nil || !state.CaughtUp {
		return
	}
	seen := cqrs.HandlerFunc[domain.ReadState, cqrs.Nothing](func(ctx context.Context, st domain.ReadState) (cqrs.Nothing, error) {
		return cqrs.Nothing{}, s.Bell.Seen(ctx, readerID, st.ConversationID, st.At)
	})
	cqrs.Announce(ctx, seen, s.Logger, "thread read", func(context.Context) ([]domain.ReadState, error) {
		return []domain.ReadState{state}, nil
	})
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

// SignStudentMessages fills each student message's sender photo, and Mine
// for reader.
func (s *Service) SignStudentMessages(ctx context.Context, readerID string, messages []domain.StudentMessage, signed map[string]*string) {
	for i := range messages {
		s.SignStudentMessage(ctx, readerID, &messages[i], signed)
	}
}

// SignStudentMessage fills m's sender photo, and Mine for reader.
func (s *Service) SignStudentMessage(ctx context.Context, readerID string, m *domain.StudentMessage, signed map[string]*string) {
	m.Sender.AvatarURL = s.Sign(ctx, m.Sender.AvatarKey, signed)
	m.Mine = m.Sender.ID == readerID
}

// AsStudent resolves teacherID as a teacher studentID may name, by the
// rules' ReadAsStudent: the facts, or domain.ErrNotFound.
func (s *Service) AsStudent(ctx context.Context, studentID, teacherID string) (domain.StudentFacts, error) {
	facts, err := s.Repo.StudentFacts(ctx, studentID, teacherID)
	if err != nil {
		return domain.StudentFacts{}, err
	}
	return facts, s.Rules.ReadAsStudent(facts)
}
