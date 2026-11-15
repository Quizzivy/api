package query

import (
	"context"
	"slices"
	"strings"

	"quizzivy/internal/modules/ai/application/internal/support"
	"quizzivy/internal/modules/ai/application/ports"
	"quizzivy/internal/modules/ai/domain"
	"quizzivy/internal/modules/ai/domain/prompts"
	"quizzivy/internal/shared/actor"
)

// VariantCallsInFlight is the most requests one variant has with the provider at the same time.
const VariantCallsInFlight = 5

// VariantCeiling is what one variant reserves: the variant's ceiling, which is
// sized for one request, once for each request the pass will make, and never
// more than one reservation may hold. A pass is then charged what it meters
// (VR-4); before, one ceiling of 300 credits covered the whole pass however
// many requests it made, and the rest went uncharged.
func VariantCeiling(calls int) int64 {
	return min(domain.FeatureVariant.Ceiling()*int64(max(calls, 1)), domain.MaxCeiling)
}

// Variant proposes rewordings, reorderings and new wrong options for the questions of the draft of TestID, standalone ones and the members of its groups alike, which the caller copies with the existing duplicate write and applies to the copy: a standalone question through a duplicate of it, a member through the copy's own group. A member's options are not reordered when its group fixes their order, and a member that answers a gap of its group's material is not reworded. It changes no answer, point or passage and writes nothing. It answers domain.ErrNotFound for a test the caller does not reach. One variant is one bounded pass, however long the draft: it asks about at most prompts.VariantChunkSize questions in each request, fewer when their prompts are long (prompts.VariantAnswerBudget), at most VariantCallsInFlight at the same time, within Service.CleanupBudget, under one reservation of VariantCeiling, the feature's ceiling for each request, that ends with one Finish of what all of them were billed. The changes of every request that answered usably are returned in paper order, so a request that failed or ran out of time leaves its questions as they are. The pass answers domain.ErrUnavailable only when no request gave a usable change.
type Variant struct {
	Actor      actor.Actor
	TestID     string
	Changes    []string
	Difficulty string
}

// Varied is the proposed changes in paper order and what the call cost, with
// what the panel says of a partial pass (VR-1, VR-5): how many questions the
// pass read, how many have a change, and whether changes past
// prompts.MaxChanges were left out.
type Varied struct {
	Changes          []prompts.Change
	Questions        int
	ChangedQuestions int
	Capped           bool
	Usage            domain.Usage
}

type VariantHandler struct{ *support.Service }

func (h VariantHandler) Handle(ctx context.Context, q Variant) (Varied, error) {
	if h.Completer == nil {
		return Varied{}, domain.ErrOff
	}
	paper, err := h.Tests.Draft(ctx, q.Actor.Scope, q.TestID)
	if err != nil {
		return Varied{}, err
	}
	calls, parts, err := prompts.VariantCalls(prompts.VariantInput{Questions: paper.Questions, Changes: q.Changes, Difficulty: q.Difficulty})
	if err != nil {
		return Varied{}, err
	}
	var changes []prompts.Change
	var capped bool
	usage, err := asker(h).sessionHolding(ctx, q.Actor, domain.FeatureVariant, ports.Tier(calls[0].Tier), VariantCeiling(len(calls)), func(s *session) error {
		var runErr error
		changes, capped, runErr = h.run(ctx, s, calls, parts)
		return runErr
	})
	if err != nil {
		return Varied{}, err
	}
	return Varied{Changes: changes, Questions: len(paper.Questions), ChangedQuestions: questionsIn(changes), Capped: capped, Usage: usage}, nil
}

// questionsIn is how many questions the changes touch.
func questionsIn(changes []prompts.Change) int {
	seen := map[string]bool{}
	for _, c := range changes {
		seen[c.QuestionID] = true
	}
	return len(seen)
}

func (h VariantHandler) run(ctx context.Context, s *session, calls []prompts.Prompt, parts []prompts.VariantInput) ([]prompts.Change, bool, error) {
	passCtx, cancel := context.WithTimeout(ctx, h.CleanupBudget)
	defer cancel()
	outcomes := s.callEach(passCtx, calls, VariantCallsInFlight)
	if ctx.Err() != nil {
		return nil, false, ctx.Err()
	}
	var changes []prompts.Change
	usable, requestIDs := 0, make([]string, 0, len(outcomes))
	var firstFailure error
	for i, o := range outcomes {
		requestIDs = append(requestIDs, loggedRequestID(o.Response))
		var failure error
		if o.Err != nil {
			failure = asker(h).failure(ctx, domain.FeatureVariant, o.Response.RequestID, o.Err)
		} else if found, readErr := prompts.DecodeVariant(o.Response.Object, parts[i]); readErr != nil {
			failure = asker(h).unusable(ctx, domain.FeatureVariant, o.Response.RequestID, readErr)
		} else {
			changes = append(changes, found...)
		}
		if failure == nil {
			usable++
		} else if firstFailure == nil {
			firstFailure = failure
		}
	}
	h.Logger.InfoContext(ctx, "ai variant", "calls", len(outcomes), "usable", usable, "spent_the_budget", passCtx.Err() != nil, "request_ids", strings.Join(requestIDs, ","))
	if usable == 0 {
		return nil, false, firstFailure
	}
	slices.SortStableFunc(changes, func(a, b prompts.Change) int { return a.Number - b.Number })
	capped := len(changes) > prompts.MaxChanges
	if capped {
		changes = changes[:prompts.MaxChanges]
	}
	return changes, capped, nil
}
