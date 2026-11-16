package command

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/google/uuid"

	"quizzivy/internal/modules/messaging/application/internal/support"
	"quizzivy/internal/modules/messaging/domain"
	"quizzivy/internal/shared/filetype"
)

// UploadAttachment stores a file a staff member will attach to a message
// (T-R7.11). It waits, unsent, until a post names it. Its type comes from its
// content, never from the part's Content-Type or the filename: PDF, Word
// (DOCX or DOC), PNG, JPEG, MP3 or M4A. A WebP answers
// domain.ErrUnsupportedType, an image whose header declares more than
// domain.MaxImageSide pixels a side domain.ErrImageDimensions, a Word file
// with macros domain.ErrHasMacros, and a file over domain.MaxAttachmentBytes
// domain.ErrTooLarge. The bytes are stored as they came. No audit row is
// written (D17).
type UploadAttachment struct {
	UploaderID string
	Filename   string
	Body       io.Reader
}

type UploadAttachmentHandler struct {
	*support.Service
}

const copyBuffer = 32 << 10

func (h UploadAttachmentHandler) Handle(ctx context.Context, cmd UploadAttachment) (domain.Attachment, error) {
	if h.Files == nil || h.Audio == nil {
		return domain.Attachment{}, domain.ErrStorageOff
	}
	if err := domain.CheckFilename(cmd.Filename); err != nil {
		return domain.Attachment{}, err
	}
	tmp, err := os.CreateTemp("", "quizzivy-attachment-*")
	if err != nil {
		return domain.Attachment{}, fmt.Errorf("messaging: temp file: %w", err)
	}
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
	}()
	hasher := sha256.New()
	size, err := boundedCopy(io.MultiWriter(tmp, hasher), cmd.Body, domain.MaxAttachmentBytes)
	if err != nil {
		return domain.Attachment{}, err
	}
	mime, err := h.identify(tmp, size)
	if err != nil {
		return domain.Attachment{}, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return domain.Attachment{}, fmt.Errorf("messaging: attachment id: %w", err)
	}
	key := domain.StorageKey(id.String(), mime)
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return domain.Attachment{}, fmt.Errorf("messaging: rewind: %w", err)
	}
	if err := h.Files.Put(ctx, key, mime, tmp, size); err != nil {
		return domain.Attachment{}, err
	}
	stored, err := h.Repo.InsertAttachment(ctx, domain.NewAttachment{
		ID: id.String(), UploadedBy: cmd.UploaderID, StorageKey: key, MimeType: mime, Bytes: size,
		Filename: domain.AttachmentFilename(cmd.Filename, mime), Checksum: hasher.Sum(nil),
	})
	if err != nil {
		_ = h.Files.Delete(ctx, key)
		return domain.Attachment{}, err
	}
	return stored, nil
}

// boundedCopy copies at most limit+1 bytes through one small buffer, so a
// file over limit is told without holding more of it: a file of exactly
// limit is accepted, one byte more answers domain.ErrTooLarge. An empty file
// is no type at all.
func boundedCopy(dst io.Writer, src io.Reader, limit int64) (int64, error) {
	n, err := io.CopyBuffer(dst, io.LimitReader(src, limit+1), make([]byte, copyBuffer))
	switch {
	case err != nil:
		return n, fmt.Errorf("messaging: reading upload: %w", err)
	case n > limit:
		return n, domain.ErrTooLarge
	case n == 0:
		return n, fmt.Errorf("%w: empty file", domain.ErrUnsupportedType)
	}
	return n, nil
}

// identify tells what the file is: an image or a PDF by its magic bytes, a
// Word file by its directory, audio through the probe. A PNG or a JPEG must
// declare at most domain.MaxImageSide pixels a side, read from its header by
// the standard library; nothing is decoded further.
func (h UploadAttachmentHandler) identify(r io.ReaderAt, size int64) (string, error) {
	head := make([]byte, filetype.HeadSize)
	n, _ := r.ReadAt(head, 0)
	switch mime := filetype.Sniff(head[:n]); mime {
	case filetype.PDF:
		return domain.MimePDF, nil
	case filetype.PNG, filetype.JPEG:
		_, _, err := filetype.Dimensions(io.NewSectionReader(r, 0, size), mime, domain.MaxImageSide)
		switch {
		case errors.Is(err, filetype.ErrDimensions):
			return "", domain.ErrImageDimensions
		case err != nil:
			return "", fmt.Errorf("%w: %w", domain.ErrUnsupportedType, err)
		}
		return mime, nil
	case filetype.WebP:
		return "", fmt.Errorf("%w: webp", domain.ErrUnsupportedType)
	}
	word, err := filetype.Word(r, size)
	switch {
	case errors.Is(err, filetype.ErrMacros):
		return "", domain.ErrHasMacros
	case word == filetype.DOCX:
		return domain.MimeDOCX, nil
	case word == filetype.DOC:
		return domain.MimeDOC, nil
	}
	return h.Audio.Audio(r, size)
}
