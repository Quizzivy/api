package prompts

import (
	"encoding/json"
	"fmt"
	"strings"

	"quizzivy/internal/modules/ai/domain"
)

// GapDistractorsInput is what "Suggest wrong options" sends for a gap in a text: the plain text of the gap's sentence on either side of the gap, the answers the gap accepts with the word the gap replaced first, the wrong options the gap already has, and the language the reasons are written in.
type GapDistractorsInput struct {
	Before   string
	After    string
	Answers  []string
	Options  []string
	Language domain.Language
}

// The most a gap's request sends of each part: the answers and options it names, and the characters of the sentence and of each of them.
const (
	MaxGapAnswers  = 20
	MaxGapOptions  = 10
	gapSentenceLen = 2000
	gapAnswerLen   = 200
	gapOptionLen   = 300
	gapMark        = "____"
)

// GapDistractors builds the request for "Suggest wrong options" on a gap in a text. It answers domain.ErrNoKey when the first answer, the word the gap replaced, holds no word.
func GapDistractors(in GapDistractorsInput) (Prompt, error) {
	if len(in.Answers) == 0 || oneLine(in.Answers[0]) == "" {
		return Prompt{}, domain.ErrNoKey
	}
	answers := distinct(in.Answers, MaxGapAnswers, gapAnswerLen)
	also := ""
	if len(answers) > 1 {
		also = "\nAlso accepted: " + strings.Join(answers[1:], " | ")
	}
	have := "none"
	if options := distinct(in.Options, MaxGapOptions, gapOptionLen); len(options) > 0 {
		have = strings.Join(options, " | ")
	}
	user := fmt.Sprintf("Sentence with a gap: \"\"\"%s\"\"\"\nCorrect answer: %s%s\nExisting wrong options: %s\n\n"+
		"Suggest %d new wrong options for the gap. Each must fit the gap's place in the sentence in form, be clearly wrong to someone who knows the rule or the text, but tempting for a learner. Match the length and part of speech of the correct answer. Do not repeat an existing option or an accepted answer, and do not suggest a word that would also be right in the sentence. "+
		"Write why in %s, in under 12 words. Put each wrong option in \"text\" and why a learner might pick it in \"why\".",
		clip(oneLine(in.Before+gapMark+in.After), gapSentenceLen), answers[0], also, have, MaxDistractors, in.Language.Name())
	return Prompt{Feature: domain.FeatureDistractors, Tier: domain.TierCapable, System: distractorsSystem, User: user, Schema: json.RawMessage(distractorsSchema), MaxTokens: 700}, nil
}

// DecodeGapDistractors reads the provider's answer for a gap. A suggestion that repeats an answer the gap accepts, an option it already has or an earlier suggestion is dropped, and no more than MaxDistractors are kept; if none is left the answer is ErrUnusable.
func DecodeGapDistractors(raw json.RawMessage, in GapDistractorsInput) ([]Distractor, error) {
	taken := make([]string, 0, len(in.Answers)+len(in.Options)+MaxDistractors)
	taken = append(taken, in.Answers...)
	taken = append(taken, in.Options...)
	return newDistractors(raw, taken)
}
