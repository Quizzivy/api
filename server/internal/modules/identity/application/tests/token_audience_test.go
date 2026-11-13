//go:build integration

package application_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"quizzivy/internal/modules/identity/application/token"
	"quizzivy/internal/shared/access"
)

// TestAnAccessTokenCarriesTheHostOfItsSession fails when IssueFor drops the
// audience, when Verify loses it, and when a token of a session opened from
// any other origin starts to name one.
func TestAnAccessTokenCarriesTheHostOfItsSession(t *testing.T) {
	i := issuer(t)
	for _, audience := range []access.Audience{access.AudienceStudent, access.AudienceStaff, access.AudienceAny, ""} {
		raw, err := i.IssueFor("user-1", 4, audience)
		if err != nil {
			t.Fatal(err)
		}
		claims, err := i.Verify(raw)
		if err != nil {
			t.Fatalf("a token for %q is refused: %v", audience, err)
		}
		want := audience
		if want == "" {
			want = access.AudienceAny
		}
		if claims.Host() != want || claims.Subject != "user-1" || claims.Epoch != 4 {
			t.Errorf("a token for %q reads as host %q, subject %q, epoch %d", audience, claims.Host(), claims.Subject, claims.Epoch)
		}
		if named := len(claims.Audience) > 0; named != (want != access.AudienceAny) {
			t.Errorf("a token for %q names the audience %v", audience, claims.Audience)
		}
	}
}

// TestAnAccessTokenForAnythingButTheTwoHostsIsRefused fails when Verify
// accepts whatever audience a token names, which would let a token minted for
// another purpose under the same key pass as an access token.
func TestAnAccessTokenForAnythingButTheTwoHostsIsRefused(t *testing.T) {
	key := []byte(strings.Repeat("k", 32))
	i, err := token.NewIssuer(key, 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for name, audience := range map[string]jwt.ClaimStrings{
		"docs":            {token.DocsAudience},
		"an unknown host": {"landing"},
		"both hosts":      {"student", "staff"},
	} {
		raw, err := jwt.NewWithClaims(jwt.SigningMethodHS256, token.Claims{RegisteredClaims: jwt.RegisteredClaims{
			Subject: "user-1", Issuer: "quizzivy", Audience: audience,
			IssuedAt: jwt.NewNumericDate(now), NotBefore: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(now.Add(time.Minute)),
		}}).SignedString(key)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := i.Verify(raw); !errors.Is(err, token.ErrTokenInvalid) {
			t.Errorf("a token for %s is accepted as an access token: %v", name, err)
		}
	}

	docs, err := token.NewDocsIssuer(key)
	if err != nil {
		t.Fatal(err)
	}
	student, err := i.IssueFor("user-1", 0, access.AudienceStudent)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := docs.Verify(student); err == nil {
		t.Error("a student session's access token opens the API reference")
	}
}
