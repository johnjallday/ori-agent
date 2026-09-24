package personalassistant

import (
	"context"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestMemoryServiceRememberReviewedHQUsesCanonicalJournalAndPreservesLegacyHomePath(t *testing.T) {
	ctx := context.Background()
	f := newKnowledgeFixture(t)
	state, err := f.relationships.GetState(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	store := NewKnowledgeStore(f.resolver(), f.folder)
	learning := NewKnowledgeLifecycleService(store, f.memory, nil)
	memory := NewMemoryService(f.relationships, f.hq, f.profileStore, f.memory)
	memory.SetReviewedHQWriter(learning)
	request := RememberRequest{IfVersion: state.StateVersion, Destination: MemoryDestinationPersonalHQ,
		Text: "Review the portfolio", RequestID: "memory-service-reviewed", Category: "projects"}
	for n := 0; n < 2; n++ {
		result, err := memory.Remember(ctx, "local", request)
		if err != nil || result.ItemID == "" || result.Href != "/profile#personalHQKnowledge" {
			t.Fatalf("reviewed Remember attempt %d: %+v %v", n, result, err)
		}
	}
	canonical, err := f.memory.Read("hq-local")
	if err != nil || len(canonical.Entries()) != 1 || !workspace.IsHQManagedProvenance(canonical.Entries()[0].Provenance) {
		t.Fatalf("reviewed Remember bypassed canonical journal: %+v %v", canonical.Entries(), err)
	}
	legacy, err := memory.Remember(ctx, "local", RememberRequest{IfVersion: state.StateVersion,
		Destination: MemoryDestinationPersonalHQ, Text: "Original confirmed Home memory"})
	if err != nil || !legacy.Created || legacy.ItemID != "" {
		t.Fatalf("historical Home Remember path changed: %+v %v", legacy, err)
	}
	canonical, err = f.memory.Read("hq-local")
	if err != nil || len(canonical.Entries()) != 2 || !strings.Contains(canonical.Entries()[1].Provenance, "user") {
		t.Fatalf("legacy Home canonical entry changed: %+v %v", canonical.Entries(), err)
	}
}
