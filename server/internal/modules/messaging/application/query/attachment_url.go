package query

import (
	"context"
	"errors"

	"quizzivy/internal/modules/messaging/application/internal/support"
	"quizzivy/internal/modules/messaging/domain"
	"quizzivy/internal/shared/access"
)

// StaffAttachmentURL mints the download URL of file ID for a staff member:
// one they uploaded and have not sent, or one a message holds in their direct
// thread, in a colleague thread they are one of the pair of, or in a class
// channel their scope reaches. Anyone else, an admin outside the thread
// included, gets domain.ErrAttachmentNotFound, as for a missing id.
type StaffAttachmentURL struct {
	ReaderID string
	Scope    access.Scope
	ID       string
}

// StudentAttachmentURL mints the download URL of file ID for a student: one a
// message holds in their direct thread, or in the channel of a class they are
// in now. Students upload nothing, so an unsent file is never theirs, and a
// colleague thread's file never is either.
type StudentAttachmentURL struct {
	StudentID string
	ID        string
}

type StaffAttachmentURLHandler struct {
	*support.Service
}

func (s StaffAttachmentURLHandler) Handle(ctx context.Context, q StaffAttachmentURL) (domain.AttachmentURL, error) {
	return attachmentURL(ctx, s.Service, q.ReaderID, q.ID, func(t domain.AttachmentThread) (bool, error) {
		switch t.Kind {
		case domain.KindDirect:
			return t.StaffID == q.ReaderID, nil
		case domain.KindColleague:
			return t.InPair(q.ReaderID), nil
		case domain.KindClass:
			return found(s.Repo.Channel(ctx, q.Scope, t.ClassID))
		}
		return false, nil
	})
}

type StudentAttachmentURLHandler struct {
	*support.Service
}

func (s StudentAttachmentURLHandler) Handle(ctx context.Context, q StudentAttachmentURL) (domain.AttachmentURL, error) {
	return attachmentURL(ctx, s.Service, q.StudentID, q.ID, func(t domain.AttachmentThread) (bool, error) {
		switch t.Kind {
		case domain.KindDirect:
			return t.StudentID == q.StudentID, nil
		case domain.KindClass:
			return found(s.Repo.StudentChannel(ctx, q.StudentID, t.ClassID))
		}
		return false, nil
	})
}

// found turns a channel read, which answers domain.ErrNotFound to a reader
// outside the channel, into whether the reader is in it.
func found[T any](_ T, err error) (bool, error) {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return false, nil
	case err != nil:
		return false, err
	}
	return true, nil
}

// attachmentURL reads file id, asks the rules whether reader may download
// it, participant deciding whether the reader takes part in the thread of
// the message that holds it, and signs a GET valid domain.AttachmentURLTTL
// that downloads it as an attachment under its name and its sniffed type.
func attachmentURL(ctx context.Context, s *support.Service, reader, id string, participant func(domain.AttachmentThread) (bool, error)) (domain.AttachmentURL, error) {
	if s.Files == nil {
		return domain.AttachmentURL{}, domain.ErrStorageOff
	}
	file, err := s.Repo.StoredAttachment(ctx, id)
	if err != nil {
		return domain.AttachmentURL{}, err
	}
	facts := domain.AttachmentFacts{Uploader: file.UploadedBy == reader, Sent: file.Thread != nil}
	if file.Thread != nil {
		if facts.Participant, err = participant(*file.Thread); err != nil {
			return domain.AttachmentURL{}, err
		}
	}
	if err := s.Rules.Download(facts); err != nil {
		return domain.AttachmentURL{}, err
	}
	url, err := s.Files.SignedAttachmentURL(ctx, file.StorageKey, file.Filename, file.MimeType, domain.AttachmentURLTTL)
	if err != nil {
		return domain.AttachmentURL{}, err
	}
	return domain.AttachmentURL{URL: url, ExpiresAt: s.Now().Add(domain.AttachmentURLTTL)}, nil
}
