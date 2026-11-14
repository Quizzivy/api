package router_test

import (
	"bufio"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"quizzivy/internal/core/router"
)

// The operations whose limiter reads a field of the body before the body
// limit does, each with the first thirteen bytes of a body it would accept.
var bodyKeyedOperations = map[string]string{
	"POST /app/classes/join":        `{"joinCode":"`,
	"POST /auth/google":             `{"joinCode":"`,
	"POST /auth/login":              `{"email":"a@b`,
	"POST /auth/set-password":       `{"token":"abc`,
	"POST /auth/set-password/check": `{"token":"abc`,
	"POST /join/preview":            `{"joinCode":"`,
}

func TestTheBodyKeyedOperationsAreTheOnesListedHere(t *testing.T) {
	var keyed []string
	limits := router.RateLimits()
	for _, pattern := range limits.Patterns() {
		if route, ok := limits.Lookup(pattern); ok && len(route.Keyed) > 0 {
			keyed = append(keyed, pattern)
		}
	}
	sort.Strings(keyed)
	listed := make([]string, 0, len(bodyKeyedOperations))
	for pattern := range bodyKeyedOperations {
		listed = append(listed, pattern)
	}
	sort.Strings(listed)
	if strings.Join(keyed, "\n") != strings.Join(listed, "\n") {
		t.Errorf("operations with a keyed bucket changed; give the new one a truncated body below.\n got: %v\nwant: %v", keyed, listed)
	}
}

// A request that declares 100 bytes, sends 13 and ends is one the server did
// not receive: 408 REQUEST_INCOMPLETE, not a 400 about its JSON. The limiter
// reads the body first on these operations, so the answer is taken over a real
// connection, where the truncation is reported to the first reader only.
func TestATruncatedBodyBehindTheLimiterIsStillRequestIncomplete(t *testing.T) {
	issuer := testIssuer(t)
	server := httptest.NewServer(roleRouter(t, issuer, rolePrincipals()))
	t.Cleanup(server.Close)
	token, err := issuer.Issue(studentUser, 0)
	if err != nil {
		t.Fatal(err)
	}

	for pattern, sent := range bodyKeyedOperations {
		t.Run(pattern, func(t *testing.T) {
			_, path, _ := strings.Cut(pattern, " ")
			status, body := truncatedOverASocket(t, server.Listener.Addr().String(), path, token, sent)

			if status != http.StatusRequestTimeout {
				t.Fatalf("status = %d, want 408: %s", status, body)
			}
			var answer struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if err := json.Unmarshal([]byte(body), &answer); err != nil {
				t.Fatalf("response is not the error envelope: %v: %s", err, body)
			}
			if answer.Error.Code != "REQUEST_INCOMPLETE" {
				t.Errorf("error.code = %q, want REQUEST_INCOMPLETE", answer.Error.Code)
			}
		})
	}
}

func truncatedOverASocket(t *testing.T, address, path, token, sent string) (int, string) {
	t.Helper()
	conn, err := net.Dial("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if err := conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}

	head := "POST " + path + " HTTP/1.1\r\n" +
		"Host: " + address + "\r\n" +
		"Authorization: Bearer " + token + "\r\n" +
		"Content-Type: application/json\r\n" +
		"Content-Length: 100\r\n\r\n"
	if _, err := io.WriteString(conn, head+sent); err != nil {
		t.Fatal(err)
	}
	if err := conn.(*net.TCPConn).CloseWrite(); err != nil {
		t.Fatal(err)
	}

	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("no answer to a truncated request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(body)
}
