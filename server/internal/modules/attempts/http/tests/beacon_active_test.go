package http_test

import (
	"context"
	"net/http"
	"strconv"
	"testing"

	"github.com/google/uuid"
	"quizzivy/gen/openapi"
	"quizzivy/internal/modules/attempts/application"
	"quizzivy/internal/modules/attempts/application/command"
	attemptshttp "quizzivy/internal/modules/attempts/http"
	"quizzivy/internal/shared/cqrs"
)

// F1: the beacon's text body carries activeMs as the JSON body does, held to
// ActiveMsInput's range; a body without it credits nothing.
func TestABeaconCarriesTheTimeOnThePaper(t *testing.T) {
	session := uuid.New()
	for name, c := range map[string]struct {
		active string
		status int
		want   *int
	}{
		"none":           {"", http.StatusAccepted, nil},
		"zero":           {`,"activeMs":0`, http.StatusAccepted, new(0)},
		"the largest":    {`,"activeMs":600000`, http.StatusAccepted, new(600000)},
		"one past it":    {`,"activeMs":600001`, http.StatusBadRequest, nil},
		"negative":       {`,"activeMs":-1`, http.StatusBadRequest, nil},
		"not an integer": {`,"activeMs":1.5`, http.StatusBadRequest, nil},
		"a string":       {`,"activeMs":"1000"`, http.StatusBadRequest, nil},
	} {
		t.Run(name, func(t *testing.T) {
			var flushed []command.Flush
			app := &application.Application{Commands: application.Commands{Flush: cqrs.HandlerFunc[command.Flush, cqrs.Nothing](func(_ context.Context, c command.Flush) (cqrs.Nothing, error) {
				flushed = append(flushed, c)
				return cqrs.Nothing{}, nil
			})}}
			transport := attemptshttp.NewAttempts(app, nil, nil, nil)
			body := `{"beaconToken":"token","sessionId":"` + session.String() + `","events":[]` + c.active + `}`
			response := serve(t, uuid.New(), func(w http.ResponseWriter, r *http.Request) {
				out, err := transport.FlushEvents(r.Context(), openapi.FlushEventsRequestObject{Id: uuid.New(), TextBody: &body})
				if err != nil {
					t.Fatal(err)
				}
				if err := out.VisitFlushEventsResponse(w); err != nil {
					t.Fatal(err)
				}
			})
			if response.Code != c.status {
				t.Fatalf("status %d, want %d: %s", response.Code, c.status, response.Body.String())
			}
			if c.status != http.StatusAccepted {
				if len(flushed) != 0 {
					t.Errorf("a refused body reached the command")
				}
				return
			}
			got := flushed[0].Input.ActiveMs
			if (got == nil) != (c.want == nil) || (got != nil && *got != *c.want) {
				t.Errorf("activeMs %s, want %s", intText(got), intText(c.want))
			}
		})
	}
}

func intText(n *int) string {
	if n == nil {
		return "none"
	}
	return strconv.Itoa(*n)
}
