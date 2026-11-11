package domain_test

import (
	"encoding/json"
	"os"
	"testing"

	"quizzivy/internal/modules/classes/domain"
)

// TestJoinCodesAreTheFormatThePinnedContractStates holds the server to
// api/catalogs/join-code.json, from @quizzivy/sdk (D-082). The web checks the
// codes a student types against the same file, so both sides agree on what a
// code is without reading each other's source.
func TestJoinCodesAreTheFormatThePinnedContractStates(t *testing.T) {
	raw, err := os.ReadFile("../../../../../../api/catalogs/join-code.json")
	if err != nil {
		t.Fatal(err)
	}
	var format struct {
		Alphabet   string `json:"alphabet"`
		Length     int    `json:"length"`
		HintLength int    `json:"hintLength"`
		GroupSize  int    `json:"groupSize"`
		Separator  string `json:"separator"`
	}
	if err := json.Unmarshal(raw, &format); err != nil {
		t.Fatal(err)
	}
	if format.Alphabet != domain.Alphabet || format.Length != domain.Length || format.HintLength != domain.HintLength {
		t.Errorf("the pinned join-code format is %+v; the server's is alphabet %q, length %d, hint %d", format, domain.Alphabet, domain.Length, domain.HintLength)
	}
	code := "K7M3P9QR"
	if want := code[:format.GroupSize] + format.Separator + code[format.GroupSize:]; domain.JoinCodes.Format(code) != want {
		t.Errorf("Format(%q) = %q, and the pinned format groups it as %q", code, domain.JoinCodes.Format(code), want)
	}
}
