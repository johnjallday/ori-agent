package folderlook

import (
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/foldermap"
)

func TestRootPolicy_CatalogAndReferenceAgreeWithAndWithoutExistingHome(t *testing.T) {
	for _, exists := range []bool{false, true} {
		ready := Readiness{HomeExists: func(string) bool { return exists }}
		facts := portfolioFacts()
		ordinary := RequiredCandidates(facts, ready)
		steps := StepsForPolicy(RootOrdinary, "Albums", facts, ordinary)
		ref := BuildReference(ReferenceInput{Facts: facts, Candidates: ordinary, Steps: steps})
		for _, line := range strings.Split(ref, "\n") {
			if strings.HasPrefix(line, "- home:") {
				if strings.Contains(line, "required, already") {
					t.Fatal(line)
				}
				want := "optional, not selected by default"
				if exists {
					want = "in Ori's default plan"
				}
				if !strings.Contains(line, want) {
					t.Fatalf("%v %s", exists, line)
				}
			}
		}
		inbox := CandidatesForPolicy(RootDownloads, facts, ready)
		allowed := StepsForPolicy(RootDownloads, "Downloads", facts, append(ordinary, inbox...))
		if len(allowed) != 1 || allowed[0].Kind != StepFileCurator || !allowed[0].Default {
			t.Fatalf("allowed=%+v", allowed)
		}
		ref = BuildReference(ReferenceInput{Policy: RootDownloads, Facts: facts, Candidates: inbox, Steps: allowed})
		if !strings.Contains(ref, "Downloads stays an inbox") || strings.Contains(ref, "[group]") || strings.Contains(ref, "[music-production-home]") || !strings.Contains(ref, "Albums/Album-6 · REAPER song") {
			t.Fatal(ref)
		}
	}
	for _, facts := range []foldermap.Facts{{}, {Dump: true}, {RootProject: &foldermap.Project{Shape: "audio", Marker: "*.rpp"}}} {
		if got := CandidatesForPolicy(RootDownloads, facts, Readiness{}); len(got) != 1 || got[0].Kind != KindFileCurator {
			t.Fatal(got)
		}
		if got := StepsForPolicy(RootUnresolved, "unknown", facts, nil); len(got) != 0 {
			t.Fatal(got)
		}
	}
}
