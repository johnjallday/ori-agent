package learningmissions

import (
	"fmt"
	"math"
	"strings"
	"testing"
)

func TestPreferenceScopeConsentRequiresExactLocalIncarnation(t *testing.T) {
	p := DefaultPreferences()
	p.Observe, p.ObservationSince, p.ConsentVersion = true, 1, DisclosureVersion
	p.WorkspaceIDs = []string{"workspace"}
	if p.Observes("workspace", "local") {
		t.Fatal("legacy workspace ID revived observation")
	}
	p.ScopeBindings = map[string]string{"workspace": "local"}
	if !p.Observes("workspace", "local") || p.Observes("workspace", "reimported") || p.Observes("foreign", "local") {
		t.Fatal("scope identity not enforced")
	}
	p.Mode = "off"
	if p.Observes("workspace", "local") {
		t.Fatal("off inherited observation")
	}
}

func TestPreferenceExclusionsAndScopesFitBoundedRecordAtWorstCase(t *testing.T) {
	p := DefaultPreferences()
	p.Focus = strings.Repeat("<", 300) // JSON HTML escaping is larger than UTF-8.
	p.Mode, p.ModelFingerprint, p.ConsentVersion = "automatic", strings.Repeat("f", 64), DisclosureVersion
	p.Observe, p.ObservationSince, p.ObservationRevision = true, math.MaxInt64, math.MaxInt64
	p.ScopeBindings = map[string]string{}
	for n := range MaxWorkspaces {
		id := fmt.Sprintf("%d%s", n, strings.Repeat("w", 127))
		p.WorkspaceIDs = append(p.WorkspaceIDs, id)
		p.ScopeBindings[id] = strings.Repeat("i", 128)
	}
	for n := range MaxRecommendationExclusions {
		p.Exclusions = append(p.Exclusions, RecommendationExclusion{WorkspaceID: p.WorkspaceIDs[0], Structure: RepeatSuccess, Topic: recommendationTopic(fmt.Sprint(n)), Until: math.MaxInt64})
	}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	if encoded, err := encode(p, MaxResponseBytes); err != nil {
		t.Fatalf("bounded preferences overflowed the 8-KiB column: %d %v", len(encoded), err)
	}
	p.Exclusions = append(p.Exclusions, p.Exclusions[0])
	if p.Validate() == nil {
		t.Fatal("unbounded exclusions accepted")
	}
	p.Exclusions = p.Exclusions[:2]
	p.Exclusions[1] = p.Exclusions[0]
	if p.Validate() == nil {
		t.Fatal("ambiguous duplicate exclusion accepted")
	}
	p.Exclusions = nil
	p.ScopeBindings[p.WorkspaceIDs[0]] = "../foreign"
	if p.Validate() == nil {
		t.Fatal("unbounded scope identity accepted")
	}
}
