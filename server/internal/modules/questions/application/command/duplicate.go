package command

import (
	"context"
	"quizzivy/internal/modules/questions/application/internal/support"
	"quizzivy/internal/modules/questions/domain"

	"github.com/google/uuid"
)

// Duplicate is A-06a's "Nhân bản": the same question again as a new bank row
// -- options, blanks, media and tags copied, ids fresh -- that no test holds
// yet. It goes through the same write as a create, so it is validated and
// audited like one. With ForTestID the copy is made in that test's owner's
// bank, as Create makes one: the caller needs edit access to the test, and may
// copy any question it can read, so a co-editor duplicates the owner's
// question inside the shared builder. Without ForTestID the copy lands in the
// caller's own bank, and that is the owner's: a caller who reads the question
// through a share gets ErrOwnerOnly, and makes copies of their own through the
// test or set instead (T-R7.7). The copy keeps the source's media, which
// reading the source authorized, so a co-editor's copy of the owner's audio
// question binds the owner's audio.
type Duplicate struct {
	Request   domain.WriteRequest
	ForTestID string
}

type DuplicateHandler struct {
	*support.Service
}

func (s DuplicateHandler) Handle(ctx context.Context, cmd Duplicate) (domain.Question, error) {
	source, err := s.Repo.Get(ctx, cmd.Request.Scope(), cmd.Request.ID)
	if err != nil {
		return domain.Question{}, err
	}
	if cmd.ForTestID == "" && !source.Access.Owner() {
		return domain.Question{}, domain.ErrOwnerOnly
	}
	req, err := s.ForTest(ctx, cmd.Request, cmd.ForTestID)
	if err != nil {
		return domain.Question{}, err
	}
	req.ID, req.SourceMedia = "", true
	req.Input = support.InputOf(source)
	if err := req.Input.RebindGaps(uuid.NewString); err != nil {
		return domain.Question{}, err
	}
	return s.Write(ctx, req, false)
}
