package application_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"quizzivy/internal/modules/ai/application/ports"
	"quizzivy/internal/modules/ai/application/query"
	"quizzivy/internal/modules/ai/domain"
	"quizzivy/internal/modules/ai/domain/prompts"
)

var askedNumber = regexp.MustCompile(`\nQ(\d+) \[`)

func longPaper(n int) domain.Paper {
	var paper domain.Paper
	for i := 1; i <= n; i++ {
		paper.Questions = append(paper.Questions, domain.PaperQuestion{
			ID: fmt.Sprintf("0198f5f0-0000-7000-8000-%012d", i), Number: i, Type: "single_choice", Prompt: fmt.Sprintf("She ___ in Hanoi since %d.", 1990+i),
			Options: []domain.Option{
				{ID: fmt.Sprintf("0198f5f0-0000-7000-8001-%012d", i), Text: "has lived", Correct: true},
				{ID: fmt.Sprintf("0198f5f0-0000-7000-8002-%012d", i), Text: "lived"},
				{ID: fmt.Sprintf("0198f5f0-0000-7000-8003-%012d", i), Text: "lives"},
			},
		})
	}
	return paper
}

func askedNumbers(req ports.Request) []int {
	var out []int
	for _, m := range askedNumber.FindAllStringSubmatch(textOf(req), -1) {
		n, _ := strconv.Atoi(m[1])
		out = append(out, n)
	}
	return out
}

func rewordEach(req ports.Request) (ports.Response, error) {
	var changes []map[string]any
	numbers := askedNumbers(req)
	for i := len(numbers) - 1; i >= 0; i-- {
		changes = append(changes, map[string]any{"question": numbers[i], "kind": "reword", "prompt": fmt.Sprintf("Reworded %d", numbers[i]), "order": []string{}, "option": "", "text": ""})
	}
	object, _ := json.Marshal(map[string]any{"changes": changes})
	return ports.Response{Object: object, Usage: perCall, RequestID: fmt.Sprintf("req_%d", numbers[0])}, nil
}

func variantOps(t *testing.T, completer ports.Completer, budget time.Duration, n int) ops {
	t.Helper()
	o := cleanupOps(t, completer, budget, nil)
	o.reads.paper = longPaper(n)
	return o
}

func reword() query.Variant {
	return query.Variant{Actor: teacher, TestID: "t-1", Changes: []string{prompts.VariantReword}, Difficulty: prompts.DifficultySame}
}

func TestAVariantOfALongTestAsksAboutAFewQuestionsAtATimeUnderOneReservation(t *testing.T) {
	completer := &scripted{respond: func(_ context.Context, req ports.Request) (ports.Response, error) { return rewordEach(req) }}
	o := variantOps(t, completer, 0, 40)

	got, err := o.app.Queries.Variant.Handle(context.Background(), reword())
	if err != nil {
		t.Fatal(err)
	}
	if len(completer.requests) != 5 {
		t.Fatalf("%d requests for 40 questions, want 5 of %d", len(completer.requests), prompts.VariantChunkSize)
	}
	seen := map[int]bool{}
	for _, req := range completer.requests {
		numbers := askedNumbers(req)
		if len(numbers) == 0 || len(numbers) > prompts.VariantChunkSize {
			t.Errorf("a request asks about questions %v, want 1 to %d of them", numbers, prompts.VariantChunkSize)
		}
		if req.MaxTokens > 4096 {
			t.Errorf("a request may write %d tokens, want an answer small enough to come back well within the pass", req.MaxTokens)
		}
		for _, n := range numbers {
			if seen[n] {
				t.Errorf("question %d is asked about twice", n)
			}
			seen[n] = true
		}
	}
	if len(got.Changes) != 40 {
		t.Fatalf("%d changes, want one for each of the 40 questions", len(got.Changes))
	}
	for i, c := range got.Changes {
		if c.Number != i+1 || c.QuestionID != o.reads.paper.Questions[i].ID {
			t.Fatalf("change %d is for question %d (%s), want the changes in paper order", i, c.Number, c.QuestionID)
		}
	}
	onlyEnded(t, o, 1)
	if o.repo.settled[0].Tokens != totalTokens(5) {
		t.Errorf("settled %+v, want what all five requests were billed", o.repo.settled[0].Tokens)
	}
}

func TestAVariantHasAtMostAFewRequestsWithTheProviderAtOnce(t *testing.T) {
	var now, most atomic.Int32
	completer := &scripted{respond: func(_ context.Context, req ports.Request) (ports.Response, error) {
		n := now.Add(1)
		for {
			m := most.Load()
			if n <= m || most.CompareAndSwap(m, n) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		now.Add(-1)
		return rewordEach(req)
	}}
	o := variantOps(t, completer, 0, 200)

	if _, err := o.app.Queries.Variant.Handle(context.Background(), reword()); err != nil {
		t.Fatal(err)
	}
	if most.Load() > query.VariantCallsInFlight || most.Load() < 2 {
		t.Errorf("%d requests at once, want them to overlap and never more than %d", most.Load(), query.VariantCallsInFlight)
	}
}

func TestAVariantKeepsTheChangesOfTheRequestsThatAnswered(t *testing.T) {
	completer := &scripted{respond: func(_ context.Context, req ports.Request) (ports.Response, error) {
		switch askedNumbers(req)[0] {
		case 9:
			return ports.Response{}, fmt.Errorf("%w: overloaded", ports.ErrUnavailable)
		case 17:
			return ports.Response{Object: json.RawMessage(`{"changes":[]}`), Usage: perCall, RequestID: "req_17"}, nil
		}
		return rewordEach(req)
	}}
	o := variantOps(t, completer, 0, 20)

	got, err := o.app.Queries.Variant.Handle(context.Background(), reword())
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Changes) != prompts.VariantChunkSize {
		t.Fatalf("%d changes, want the %d of the one request that answered usably", len(got.Changes), prompts.VariantChunkSize)
	}
	for _, c := range got.Changes {
		if c.Number > prompts.VariantChunkSize {
			t.Errorf("a change to question %d, which no usable answer named", c.Number)
		}
	}
	onlyEnded(t, o, 1)
	if o.repo.settled[0].Tokens != totalTokens(2) {
		t.Errorf("settled %+v, want the two billed answers and not the failed request", o.repo.settled[0].Tokens)
	}
}

func TestAVariantWithNoUsableAnswerIsUnavailable(t *testing.T) {
	completer := &scripted{respond: func(context.Context, ports.Request) (ports.Response, error) {
		return ports.Response{Object: json.RawMessage(`{"changes":[]}`), Usage: perCall, RequestID: "req_none"}, nil
	}}
	o := variantOps(t, completer, 0, 20)

	_, err := o.app.Queries.Variant.Handle(context.Background(), reword())
	if !errors.Is(err, domain.ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
	onlyEnded(t, o, 1)
}

func TestAVariantThatRunsOutOfTimeReturnsWhatItHas(t *testing.T) {
	completer := &scripted{respond: func(ctx context.Context, req ports.Request) (ports.Response, error) {
		if askedNumbers(req)[0] == 1 {
			return rewordEach(req)
		}
		<-ctx.Done()
		return ports.Response{}, fmt.Errorf("%w: %w", ports.ErrUnavailable, ctx.Err())
	}}
	o := variantOps(t, completer, 150*time.Millisecond, 24)

	start := time.Now()
	got, err := o.app.Queries.Variant.Handle(context.Background(), reword())
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 5*time.Second {
		t.Errorf("took %v, want the pass to end at its budget", time.Since(start))
	}
	if len(got.Changes) != prompts.VariantChunkSize {
		t.Errorf("%d changes, want the first request's %d", len(got.Changes), prompts.VariantChunkSize)
	}
	onlyEnded(t, o, 1)
}

// The three ways a variant pass ends that nothing covered (VR-10): the
// provider's client panics, the caller goes away, and the pass costs more than
// the one reservation held for it.

func TestAPanicInOneVariantRequestStillEndsTheReservationOnceWithWhatWasBilled(t *testing.T) {
	for name, c := range map[string]struct {
		panics  func(ports.Request) bool
		settles int
	}{
		"one of three requests": {func(req ports.Request) bool { return askedNumbers(req)[0] == 9 }, 1},
		"every one of them":     {func(ports.Request) bool { return true }, 0},
	} {
		t.Run(name, func(t *testing.T) {
			completer := &scripted{respond: func(_ context.Context, req ports.Request) (ports.Response, error) {
				if c.panics(req) {
					panic("the provider client crashed")
				}
				return rewordEach(req)
			}}
			o := variantOps(t, completer, 0, 24)
			var recovered any
			func() {
				defer func() { recovered = recover() }()
				_, _ = o.app.Queries.Variant.Handle(context.Background(), reword())
			}()
			if recovered == nil || !strings.Contains(fmt.Sprint(recovered), "the provider client crashed") {
				t.Fatalf("recovered %v, want the provider's panic to reach the caller", recovered)
			}
			if asked := len(completer.seen()); asked != 3 {
				t.Fatalf("%d requests were made, want all three: a panic in one does not stop the others", asked)
			}
			onlyEnded(t, o, c.settles)
			if c.settles == 1 && o.repo.settled[0].Tokens != totalTokens(2) {
				t.Errorf("settled %+v, want the two requests that returned", o.repo.settled[0].Tokens)
			}
		})
	}
}

func TestACancelledVariantSettlesWhatWasBilledOnceAndReleasesTheRest(t *testing.T) {
	for name, c := range map[string]struct {
		settles int
	}{
		"after one request answered": {1},
		"before any answered":        {0},
	} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var started atomic.Int32
			answeredOne := make(chan struct{})
			var once sync.Once
			completer := &scripted{respond: func(_ context.Context, req ports.Request) (ports.Response, error) {
				started.Add(1)
				if c.settles == 1 && askedNumbers(req)[0] == 1 {
					defer once.Do(func() { close(answeredOne) })
					return rewordEach(req)
				}
				<-ctx.Done()
				return ports.Response{}, ctx.Err()
			}}
			o := variantOps(t, completer, 0, 24)
			go func() {
				for started.Load() < 3 {
					time.Sleep(time.Millisecond)
				}
				if c.settles == 1 {
					<-answeredOne
				}
				cancel()
			}()
			got, err := o.app.Queries.Variant.Handle(ctx, reword())
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("err = %v, want the cancellation", err)
			}
			if len(got.Changes) != 0 {
				t.Errorf("a cancelled variant returned %d changes, want none: nobody is there to apply them", len(got.Changes))
			}
			onlyEnded(t, o, c.settles)
			if c.settles == 1 && o.repo.settled[0].Tokens != totalTokens(1) {
				t.Errorf("settled %+v, want the one request that answered", o.repo.settled[0].Tokens)
			}
		})
	}
}

// One reservation of the feature's ceiling covers every request of the pass
// (VR-4 describes the rule; this pins it). A pass that the provider metered
// above it is settled once with everything metered, the teacher is charged
// what was held, the overrun is logged, and no change is lost to it.
func TestAVariantThatCostsMoreThanItsCeilingIsChargedWhatWasHeldAndSaysSo(t *testing.T) {
	completer := &scripted{respond: func(_ context.Context, req ports.Request) (ports.Response, error) { return rewordEach(req) }}
	o := variantOps(t, completer, 0, 40)
	ceiling := domain.FeatureVariant.Ceiling()
	// testPrices: one credit an input token and two an output token on the capable tier.
	metered := 5 * (perCall.InputTokens + 2*perCall.OutputTokens)
	if metered <= ceiling {
		t.Fatalf("five requests meter %d credits, which is within the ceiling of %d: the fixture proves nothing", metered, ceiling)
	}
	o.repo.settleOut = domain.Settled{Charged: ceiling, Metered: metered}

	got, err := o.app.Queries.Variant.Handle(context.Background(), reword())
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Changes) != 40 {
		t.Errorf("%d changes, want all 40: the overrun is the centre's cost, not the teacher's loss", len(got.Changes))
	}
	onlyEnded(t, o, 1)
	if held := o.repo.reserved[0]; held.Feature != domain.FeatureVariant || held.Credits != ceiling {
		t.Errorf("reserved %+v, want the variant's ceiling of %d once for the whole pass", held, ceiling)
	}
	if s := o.repo.settled[0]; s.Metered != metered || s.Tokens != totalTokens(5) || s.Reservation.Credits != ceiling {
		t.Errorf("settlement %+v, want the %d credits and the tokens of all five requests against the %d held", s, metered, ceiling)
	}
	if got.Usage.Credits != ceiling {
		t.Errorf("the answer says the call cost %d credits, want the %d that were charged", got.Usage.Credits, ceiling)
	}
	logs := o.logs.String()
	for _, want := range []string{"level=WARN", `msg="ai call cost more than its ceiling"`, "feature=variant", fmt.Sprintf("ceiling=%d", ceiling), fmt.Sprintf("metered=%d", metered)} {
		if !strings.Contains(logs, want) {
			t.Errorf("the log lacks %s:\n%s", want, logs)
		}
	}
}
