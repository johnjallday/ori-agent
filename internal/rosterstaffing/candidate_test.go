package rosterstaffing

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/store"
)

func candidateReaders(profiles ...store.StaffingProfile) Readers {
	return Readers{
		Profiles: func(context.Context) store.StaffingSnapshot {
			return store.StaffingSnapshot{Profiles: profiles, Complete: true}
		},
		ModelConfigured: func(provider, model string) bool { return provider == "anthropic" && model != "" },
	}
}

func testProfile(name string) store.StaffingProfile {
	return store.StaffingProfile{Name: name, Source: "root", Witness: "private-witness-" + name, Purpose: "Coordinates music portfolios", Role: "orchestrator", Provider: "anthropic", Model: "claude-sonnet", Capabilities: []string{"planning"}}
}

func TestCollectProjectionPrivacyAndCoverage(t *testing.T) {
	p := testProfile("Unrelated display name")
	p.Capabilities = append(p.Capabilities, "/private/config", "sk-secret", "planning")
	readers := candidateReaders(p)
	got := Collect(context.Background(), "local", readers)
	if !got.Coverage.Complete || got.Coverage.AssignmentsComplete || len(got.Candidates) != 1 {
		t.Fatalf("coverage: %+v", got.Coverage)
	}
	c := got.Candidates[0]
	if !c.Ready || !hasPurpose(c) || len(c.capabilities) != 1 {
		t.Fatalf("projection: %+v", c)
	}
	data, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{p.Name, p.Witness, "/private/config", "sk-secret", "claude-sonnet"} {
		if strings.Contains(string(data), forbidden) {
			t.Fatalf("private/model-unnecessary value %q serialized", forbidden)
		}
	}
	other := Collect(context.Background(), "other", readers)
	if other.Candidates[0].Handle == c.Handle {
		t.Fatal("handle not owner-bound")
	}
}

func TestCollectNoAlphabeticalFiftyCap(t *testing.T) {
	profiles := make([]store.StaffingProfile, 80)
	for i := range profiles {
		profiles[i] = testProfile(fmt.Sprintf("Agent %03d", i))
	}
	profiles[79].Name = "Zebra"
	got := Collect(context.Background(), "local", candidateReaders(profiles...))
	if !got.Coverage.Complete || len(got.Candidates) != 80 {
		t.Fatalf("lost coverage: %+v", got.Coverage)
	}
	found := false
	for _, c := range got.Candidates {
		found = found || c.Name == "Zebra"
	}
	if !found {
		t.Fatal("alphabetical tail was dropped")
	}
	for i := 0; i < 30; i++ {
		profiles = append(profiles, testProfile(fmt.Sprintf("Extra %d", i)))
	}
	got = Collect(context.Background(), "local", candidateReaders(profiles...))
	if got.Coverage.Complete || got.Coverage.Inspected != 110 || len(got.Candidates) != MaxCandidates {
		t.Fatalf("budget was hidden: %+v", got.Coverage)
	}
}

func TestCollectRejectsUnknownPrivateAndAmbiguousSources(t *testing.T) {
	profiles := []store.StaffingProfile{testProfile("private"), testProfile("unknown"), testProfile("missing"), testProfile("Same"), testProfile("same")}
	profiles[0].Source = "workspace"
	profiles[1].Source = "foreign"
	profiles[2].Witness = ""
	got := Collect(context.Background(), "local", candidateReaders(profiles...))
	if got.Coverage.Complete || len(got.Candidates) != 0 {
		t.Fatalf("ineligible projection: %+v", got)
	}
}

func TestCollectAssignmentsRequireWitnessAndFreshOwnerScope(t *testing.T) {
	p := testProfile("Scout")
	readers := candidateReaders(p)
	trusted := true
	readers.Assignments = func(_ context.Context, owner string) ([]Assignment, bool) {
		if owner != "local" || !trusted {
			return nil, false
		}
		return []Assignment{{Witness: p.Witness, RoleID: "manager", WorkspaceLabel: "Music"}, {Witness: "foreign", RoleID: "manager", WorkspaceLabel: "Other owner's private Home"}}, true
	}
	got := Collect(context.Background(), "local", readers)
	if !got.Coverage.AssignmentsComplete || len(got.Candidates[0].Evidence) != 4 {
		t.Fatalf("assignment evidence: %+v", got)
	}
	trusted = false
	got = Collect(context.Background(), "local", readers)
	if got.Coverage.AssignmentsComplete || len(got.Candidates[0].Evidence) != 3 {
		t.Fatal("revoked metadata survived collection")
	}
}

func TestCollectUnsafeDescriptionsAndMissingReadiness(t *testing.T) {
	for _, purpose := range []string{"", "read /private/settings", "use api_key=CANARY", "use ori_local_config_id", "use sk-CANARY", "bad\x00text"} {
		p := testProfile("Scout")
		p.Purpose = purpose
		p.Paused = true
		got := Collect(context.Background(), "local", candidateReaders(p)).Candidates[0]
		if hasPurpose(got) || got.Ready {
			t.Fatalf("unsafe purpose/readiness: %+v", got)
		}
	}
	p := testProfile("Scout")
	p.Purpose = strings.Repeat("x", 800)
	got := Collect(context.Background(), "local", candidateReaders(p)).Candidates[0]
	if len([]rune(got.Evidence[0].Value)) != MaxPurposeRunes {
		t.Fatal("unbounded description")
	}
	if got := Collect(context.Background(), "", candidateReaders(p)); got.Coverage.Complete || len(got.Candidates) != 0 {
		t.Fatal("missing owner accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := Collect(ctx, "local", candidateReaders(p)); got.Coverage.Complete {
		t.Fatal("cancelled collection succeeded")
	}
}
