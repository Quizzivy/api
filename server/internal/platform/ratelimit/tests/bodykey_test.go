package ratelimit_test

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"quizzivy/internal/platform/ratelimit"
)

func post(body string) *http.Request {
	return httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(body))
}

func TestExtractsTheField(t *testing.T) {
	key := ratelimit.JSONFieldKey("email", 4096, 254)
	if got := key(post(`{"email":"a@b.co","password":"x"}`)); got != "a@b.co" {
		t.Errorf("got %q", got)
	}
}

func TestTheHandlerStillSeesTheWholeBody(t *testing.T) {
	body := `{"email":"a@b.co","password":"hunter22"}`
	r := post(body)
	ratelimit.JSONFieldKey("email", 4096, 254)(r)

	got, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != body {
		t.Errorf("handler would read %q, want %q", got, body)
	}
}

func TestCaseAndWhitespaceShareABucket(t *testing.T) {
	key := ratelimit.JSONFieldKey("email", 4096, 254)
	a := key(post(`{"email":"  A@B.CO "}`))
	b := key(post(`{"email":"a@b.co"}`))
	if a != b {
		t.Errorf("%q and %q landed in different buckets", a, b)
	}
}

func TestOversizedBodyYieldsNoKeyButIsStillReadable(t *testing.T) {
	big := `{"email":"a@b.co","pad":"` + strings.Repeat("x", 5000) + `"}`
	r := post(big)
	if got := ratelimit.JSONFieldKey("email", 1024, 254)(r); got != "" {
		t.Errorf("oversized body produced key %q; the route's body limit refuses it instead", got)
	}
	got, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != big {
		t.Errorf("handler lost part of an oversized body: got %d bytes, want %d", len(got), len(big))
	}
}

func TestMalformedOrMissingYieldsNoKey(t *testing.T) {
	key := ratelimit.JSONFieldKey("email", 4096, 254)
	for name, body := range map[string]string{
		"not json":     `not json at all`,
		"missing":      `{"password":"x"}`,
		"wrong type":   `{"email":123}`,
		"empty body":   ``,
		"null value":   `{"email":null}`,
		"nested array": `[{"email":"a@b.co"}]`,
	} {
		t.Run(name, func(t *testing.T) {
			if got := key(post(body)); got != "" {
				t.Errorf("got %q, want no key", got)
			}
		})
	}
}

func TestTrailingBytesDoNotHideTheField(t *testing.T) {
	key := ratelimit.JSONFieldKey("email", 4096, 254)
	for name, body := range map[string]string{
		"a trailing byte":        `{"email":"victim@example.com","password":"x"}x`,
		"a second value":         `{"email":"victim@example.com"} {"email":"other@example.com"}`,
		"whitespace padding":     `{"email":"victim@example.com",` + strings.Repeat(" ", 2000) + `"password":"x"}`,
		"an escaped member name": `{"\u0065mail":"victim@example.com"}`,
	} {
		if got := key(post(body)); got != "victim@example.com" {
			t.Errorf("%s: key %q, want the email the handler reads", name, got)
		}
	}
}

func TestAValueLongerThanTheSchemaAllowsYieldsNoKey(t *testing.T) {
	key := ratelimit.JSONFieldKey("email", 4096, 254)
	if got := key(post(`{"email":"` + strings.Repeat("a", 250) + `@b.co"}`)); got != "" {
		t.Errorf("a 255-rune email produced key of %d bytes; validation refuses it, so it must not fill a bucket", len(got))
	}
	if got := key(post(`{"email":"` + strings.Repeat("a", 249) + `@b.co"}`)); got == "" {
		t.Error("a 254-rune email produced no key")
	}
	code := ratelimit.JSONFieldKeyFunc("joinCode", 4096, 9, strings.ToUpper)
	if got := code(post(`{"joinCode":"k7m3-p9qr"}`)); got != "K7M3-P9QR" {
		t.Errorf("a nine-rune code gave %q", got)
	}
	if got := code(post(`{"joinCode":"` + strings.Repeat("K", 4000) + `"}`)); got != "" {
		t.Errorf("an over-long code produced a key of %d bytes", len(got))
	}
}

// cutShort is a request body as net/http hands it over when the declared
// length ends early: the bytes that arrived, the truncation reported once,
// then a clean end on every later read.
type cutShort struct {
	arrived  io.Reader
	reported bool
}

func (b *cutShort) Read(p []byte) (int, error) {
	if n, err := b.arrived.Read(p); n > 0 || err == nil {
		return n, nil
	}
	if b.reported {
		return 0, io.EOF
	}
	b.reported = true
	return 0, io.ErrUnexpectedEOF
}

func TestABodyCutShortYieldsNoKeyAndStillFailsForTheNextReader(t *testing.T) {
	const arrived = `{"email":"a@b`
	key := ratelimit.JSONFieldKey("email", 4096, 254)
	r := httptest.NewRequest(http.MethodPost, "/auth/login", &cutShort{arrived: strings.NewReader(arrived)})

	for reader := 1; reader <= 2; reader++ {
		if got := key(r); got != "" {
			t.Errorf("key reader %d: a body cut short produced key %q, want none", reader, got)
		}
	}
	for reader := 1; reader <= 2; reader++ {
		got, err := io.ReadAll(r.Body)
		if !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Errorf("reader %d behind the limiter: err = %v, want the truncation the key reader met", reader, err)
		}
		if reader == 1 && string(got) != arrived {
			t.Errorf("reader 1 behind the limiter read %q, want the bytes that arrived, %q", got, arrived)
		}
	}
}

func TestAReadErrorPastTheKeyLimitIsLeftToTheNextReader(t *testing.T) {
	big := `{"email":"a@b.co","pad":"` + strings.Repeat("x", 5000)
	r := httptest.NewRequest(http.MethodPost, "/auth/login", &cutShort{arrived: strings.NewReader(big)})
	if got := ratelimit.JSONFieldKey("email", 1024, 254)(r); got != "" {
		t.Errorf("oversized body produced key %q", got)
	}
	got, err := io.ReadAll(r.Body)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Errorf("err = %v, want the body's own truncation, which the key reader never reached", err)
	}
	if string(got) != big {
		t.Errorf("the next reader got %d bytes, want %d", len(got), len(big))
	}
}
