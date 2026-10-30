package domain_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"quizzivy/internal/modules/audit/domain"
)

var computedActions = []string{"ai.credit_request.approved", "ai.credit_request.declined"}

func catalogued() map[string]domain.Action {
	out := map[string]domain.Action{}
	for _, a := range domain.Actions() {
		out[a.Key] = a
	}
	return out
}

type pinnedAction struct {
	Key string `json:"key"`
	VI  string `json:"vi"`
	EN  string `json:"en"`
}

// pinned reads the audit catalog of the pinned contract, api/catalogs/audit-actions.json
// (from @quizzivy/sdk, D-082). The web words its audit.action keys from the same file, so
// the search, which matches the catalogue's sentences, finds what the screen shows.
func pinned(t *testing.T) []pinnedAction {
	t.Helper()
	path := filepath.Join(serverRoot(t), "..", "api", "catalogs", "audit-actions.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var catalog struct {
		Actions []pinnedAction `json:"actions"`
	}
	if err := json.Unmarshal(raw, &catalog); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return catalog.Actions
}

func TestEveryActionTheServerWritesIsInTheCatalogue(t *testing.T) {
	actions, _ := scanServer(t)
	known := catalogued()
	for _, name := range sortedNames(actions) {
		if _, ok := known[name]; !ok {
			t.Errorf("the server writes the action %q (%s) and modules/audit/domain/actions.go does not list it: add it with its Vietnamese and English sentence, and the same entry to catalogs/audit-actions.json in Quizzivy/sdk", name, actions[name][0])
		}
	}
	if len(actions) < 60 {
		t.Errorf("the scan found only %d actions: it is looking in the wrong place", len(actions))
	}
}

func TestEveryActionInTheCatalogueIsStillWritten(t *testing.T) {
	actions, _ := scanServer(t)
	for _, a := range domain.Actions() {
		if _, written := actions[a.Key]; !written && !slices.Contains(computedActions, a.Key) {
			t.Errorf("the catalogue holds %q, which no code writes any more: remove it here and from catalogs/audit-actions.json in Quizzivy/sdk", a.Key)
		}
	}
}

func TestEveryEntityTheServerWritesHasACategoryOrIsAllOnly(t *testing.T) {
	_, entities := scanServer(t)
	for _, name := range sortedNames(entities) {
		_, categorised := domain.CategoryOf(name)
		if !categorised && !domain.AllOnly(name) {
			t.Errorf("the server writes entries about %q (%s) and modules/audit/domain/categories.go neither puts it in a category nor lists it as All-only", name, entities[name][0])
		}
	}
	if len(entities) < 15 {
		t.Errorf("the scan found only %d entities: it is looking in the wrong place", len(entities))
	}
}

func TestNoEntityIsBothInACategoryAndAllOnly(t *testing.T) {
	for _, entity := range domain.Entities() {
		_, categorised := domain.CategoryOf(entity)
		if categorised && domain.AllOnly(entity) {
			t.Errorf("%q is in a category and also listed as All-only", entity)
		}
	}
	seen := map[string]bool{}
	for _, entity := range domain.Entities() {
		if seen[entity] {
			t.Errorf("%q is listed twice", entity)
		}
		seen[entity] = true
	}
}

func TestTheCatalogueIsInKeyOrderWithOneSentencePerLanguage(t *testing.T) {
	keys := make([]string, 0, len(domain.Actions()))
	for _, a := range domain.Actions() {
		keys = append(keys, a.Key)
		if !actionShape.MatchString(a.Key) {
			t.Errorf("%q is not of the form noun.verb", a.Key)
		}
		for lang, sentence := range map[string]string{"vi": a.VI, "en": a.EN} {
			if strings.TrimSpace(sentence) == "" || strings.HasSuffix(sentence, ".") || sentence != strings.TrimSpace(sentence) {
				t.Errorf("%s sentence of %q is %q: it is blank, ends in a full stop or has spaces around it", lang, a.Key, sentence)
			}
		}
	}
	if !sort.StringsAreSorted(keys) {
		t.Errorf("modules/audit/domain/actions.go is not in key order")
	}
	if compact := slices.Compact(slices.Clone(keys)); len(compact) != len(keys) {
		t.Errorf("an action is listed twice")
	}
}

func TestThePinnedCatalogIsTheCatalogue(t *testing.T) {
	want := domain.Actions()
	got := pinned(t)
	if len(got) != len(want) {
		t.Errorf("the pinned catalog lists %d actions and modules/audit/domain/actions.go %d: change the catalog in Quizzivy/sdk and bump api/sdk-version in the same release", len(got), len(want))
	}
	for i := range min(len(got), len(want)) {
		if got[i] != (pinnedAction{want[i].Key, want[i].VI, want[i].EN}) {
			t.Errorf("action %d is %+v in the pinned catalog and %+v here: they must be the same, in the same order", i, got[i], want[i])
		}
	}
}

func sortedNames(f found) []string {
	out := f.names()
	sort.Strings(out)
	return out
}
