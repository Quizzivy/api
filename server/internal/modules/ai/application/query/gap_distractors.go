package query

import (
	"context"

	"quizzivy/internal/modules/ai/application/internal/support"
	"quizzivy/internal/modules/ai/domain"
	"quizzivy/internal/modules/ai/domain/prompts"
	"quizzivy/internal/shared/actor"
)

// GapDistractors suggests wrong options for a gap in a text from what the caller sends of it, saved or not: the gap's sentence on either side of the gap, the answers the gap accepts with the word it replaced first, and the wrong options it already has. It reads nothing stored and writes nothing. It answers domain.ErrNoKey when the first answer holds no word.
type GapDistractors struct {
	Actor   actor.Actor
	Before  string
	After   string
	Answers []string
	Options []string
}

type GapDistractorsHandler struct{ *support.Service }

func (h GapDistractorsHandler) Handle(ctx context.Context, q GapDistractors) (Suggested, error) {
	if h.Completer == nil {
		return Suggested{}, domain.ErrOff
	}
	in := prompts.GapDistractorsInput{Before: q.Before, After: q.After, Answers: q.Answers, Options: q.Options, Language: h.LanguageFor(ctx, q.Actor.ID, "")}
	prompt, err := prompts.GapDistractors(in)
	if err != nil {
		return Suggested{}, err
	}
	got, err := asker(h).ask(ctx, q.Actor, prompt)
	if err != nil {
		return Suggested{}, err
	}
	items, err := prompts.DecodeGapDistractors(got.Object, in)
	if err != nil {
		return Suggested{}, asker(h).unusable(ctx, domain.FeatureDistractors, got.RequestID, err)
	}
	return Suggested{Items: items, Usage: got.Usage}, nil
}
