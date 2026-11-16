package command

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"quizzivy/internal/modules/messaging/application/internal/support"
	"quizzivy/internal/modules/messaging/application/ports"
	"quizzivy/internal/modules/messaging/domain"
)

// SweepUnsentAttachments removes at most Limit files uploaded more than
// domain.UnsentGrace ago that no message holds, oldest first, and returns
// how many rows it removed: the row first, then the object, so a failed
// object delete leaves only an object no row names, which
// SweepAttachmentObjects removes later. A sent file is never removed: the
// rows come from DeleteUnsentAttachments, and app.messages' RESTRICT key
// refuses to let a held one go. Every object of the batch is tried, and the
// first failure is the error. It is one step of the daily housekeeping,
// repeated while it returns Limit.
type SweepUnsentAttachments struct {
	Limit int
}

type SweepUnsentAttachmentsHandler struct {
	*support.Service
}

func (h SweepUnsentAttachmentsHandler) Handle(ctx context.Context, c SweepUnsentAttachments) (int, error) {
	if c.Limit <= 0 {
		return 0, errors.New("sweep unsent attachments: limit must be positive")
	}
	if h.Files == nil {
		return 0, nil
	}
	keys, err := h.Repo.DeleteUnsentAttachments(ctx, h.Now().Add(-domain.UnsentGrace), c.Limit)
	if err != nil {
		return 0, fmt.Errorf("sweep unsent attachments: %w", err)
	}
	var first error
	for _, key := range keys {
		if err := h.Files.Delete(ctx, key); err != nil && first == nil {
			first = fmt.Errorf("sweep unsent attachments: delete an object: %w", err)
		}
	}
	return len(keys), first
}

// SweepAttachmentObjects removes the objects under domain.AttachmentPrefix
// that no file's row names and that were written more than
// domain.UnsentGrace ago: those T-R7.12's anonymisation leaves, which deletes
// rows without storage credentials, and those a failed delete left. It
// removes at most Limit and returns how many. It lists a page of Limit keys at
// a time from where the last call stopped, at most SweepAttachmentPages pages
// a call, and starts over from the first key once a listing comes back short,
// so a bucket of many sent files is walked across calls and runs rather than
// from its start each time, and one call's work does not grow with the
// bucket. The rows are read after the listing, so an object a row names
// meanwhile is kept, and the grace keeps an upload whose row is not written
// yet. Every removable object of a page is tried, and the first failure is
// the error; a failure to read the rows stops the call where it is, so the
// next one reads that page again.
type SweepAttachmentObjects struct {
	Limit int
}

// SweepAttachmentPages is how many pages one SweepAttachmentObjects call
// lists at most.
const SweepAttachmentPages = 10

type SweepAttachmentObjectsHandler struct {
	*support.Service
	Cursor *SweepCursor
}

// SweepCursor is where SweepAttachmentObjects resumes its listing: the last
// key it looked at, empty to start from the first.
type SweepCursor struct {
	mu    sync.Mutex
	after string
}

func (h SweepAttachmentObjectsHandler) Handle(ctx context.Context, c SweepAttachmentObjects) (int, error) {
	if c.Limit <= 0 {
		return 0, errors.New("sweep attachment objects: limit must be positive")
	}
	if h.Files == nil {
		return 0, nil
	}
	h.Cursor.mu.Lock()
	defer h.Cursor.mu.Unlock()
	removed := 0
	var first error
	for range SweepAttachmentPages {
		if err := ctx.Err(); err != nil {
			return removed, errors.Join(first, err)
		}
		objects, err := h.Files.ListAfter(ctx, domain.AttachmentPrefix, h.Cursor.after, c.Limit)
		if err != nil {
			return removed, errors.Join(first, fmt.Errorf("sweep attachment objects: list: %w", err))
		}
		cutoff := h.Now().Add(-domain.UnsentGrace)
		named, err := h.Repo.NamedKeys(ctx, olderThan(objects, cutoff))
		if err != nil {
			// The cursor has not moved: stop, and the next call reads this
			// page again.
			return removed, errors.Join(first, fmt.Errorf("sweep attachment objects: %w", err))
		}
		n, err := h.removeUnnamed(ctx, objects, named, cutoff, c.Limit-removed)
		removed += n
		if first == nil {
			first = err
		}
		if len(objects) < c.Limit {
			h.Cursor.after = ""
			return removed, first
		}
		if removed >= c.Limit {
			return removed, first
		}
	}
	return removed, first
}

func olderThan(objects []ports.StoredObject, cutoff time.Time) []string {
	var old []string
	for _, o := range objects {
		if o.WrittenAt.Before(cutoff) {
			old = append(old, o.Key)
		}
	}
	return old
}

// removeUnnamed removes up to limit of objects that were written before
// cutoff and that named does not hold, and moves the cursor past the last
// object it looked at.
func (h SweepAttachmentObjectsHandler) removeUnnamed(ctx context.Context, objects []ports.StoredObject, named map[string]bool, cutoff time.Time, limit int) (int, error) {
	removed := 0
	var first error
	for _, o := range objects {
		h.Cursor.after = o.Key
		if !o.WrittenAt.Before(cutoff) || named[o.Key] {
			continue
		}
		if err := h.Files.Delete(ctx, o.Key); err != nil {
			if first == nil {
				first = fmt.Errorf("sweep attachment objects: delete an object: %w", err)
			}
			continue
		}
		removed++
		if removed == limit {
			break
		}
	}
	return removed, first
}
