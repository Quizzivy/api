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
// bank, as Create makes one.
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
	cmd.Request.ID = ""
	cmd.Request.Input = support.InputOf(source)
	if err := cmd.Request.Input.RebindGaps(uuid.NewString); err != nil {
		return domain.Question{}, err
	}
	req, err := s.ForTest(ctx, cmd.Request, cmd.ForTestID)
	if err != nil {
		return domain.Question{}, err
	}
	return s.Write(ctx, req, false)
}
