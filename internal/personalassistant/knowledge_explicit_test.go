package personalassistant

import (
	"context"
	"strings"
	"testing"
)

func TestSaveExplicitIsCanonicalExactConfirmedAndSurvivesRetryForgetAndReentry(t *testing.T) {
	f := newKnowledgeFixture(t)
	ctx := context.Background()
	knowledge := NewKnowledgeStore(f.resolver(), f.folder)
	svc := NewKnowledgeLifecycleService(knowledge, f.memory, nil)
	binding, err := knowledge.resolve(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SaveExplicit(ctx, "local", binding.StateVersion+1, "explicit-1", "projects", "Finish the portfolio"); err == nil {
		t.Fatal("stale relationship version saved an explicit fact")
	}
	if snapshot, err := f.memory.SnapshotExact(binding.HQWorkspaceID); err != nil || len(snapshot.Entries) != 0 {
		t.Fatalf("failed save wrote canonical memory: %+v %v", snapshot.Entries, err)
	}
	first, err := svc.SaveExplicit(ctx, "local", binding.StateVersion, "explicit-1", "projects", "Finish the portfolio")
	if err != nil || first.State != KnowledgeApproved || first.Target == nil || !strings.HasPrefix(first.Target.Marker, "ori-hq:") {
		t.Fatalf("explicit save=%+v %v", first, err)
	}
	reader := NewKnowledgeContextReader(knowledge, f.memory, nil)
	section, err := reader.Section(ctx, "local", binding.HQWorkspaceID, binding.EntryAgentInstanceID)
	if err != nil || strings.Count(section, "Finish the portfolio") != 1 {
		t.Fatalf("canonical explicit fact absent or repeated: %q %v", section, err)
	}
	restarted := NewKnowledgeLifecycleService(NewKnowledgeStore(f.resolver(), f.folder), f.memory, nil)
	replay, err := restarted.SaveExplicit(ctx, "local", binding.StateVersion, "explicit-1", "projects", "Finish the portfolio")
	if err != nil || replay.ID != first.ID {
		t.Fatalf("lost-response retry created another fact: %+v %v", replay, err)
	}
	if _, err := restarted.SaveExplicit(ctx, "local", binding.StateVersion, "explicit-1", "projects", "Changed content"); err == nil {
		t.Fatal("same request ID accepted changed statement")
	}
	if _, err := restarted.SaveExplicit(ctx, "local", binding.StateVersion, "explicit-2", "projects", "Finish the portfolio"); err == nil {
		t.Fatal("same canonical text duplicated with a new marker")
	}
	forgotten, err := restarted.ForgetItem(ctx, "local", first.ID, first.Version, "forget-explicit")
	if err != nil || forgotten.State != KnowledgeForgotten || len(forgotten.Revisions) != 0 {
		t.Fatalf("forget explicit=%+v %v", forgotten, err)
	}
	again, err := restarted.SaveExplicit(ctx, "local", binding.StateVersion, "explicit-3", "projects", "Finish the portfolio")
	if err != nil || again.State != KnowledgeApproved || again.ID == first.ID {
		t.Fatalf("explicit re-entry after Forget: %+v %v", again, err)
	}
	section, err = reader.Section(ctx, "local", binding.HQWorkspaceID, binding.EntryAgentInstanceID)
	if err != nil || strings.Count(section, "Finish the portfolio") != 1 {
		t.Fatalf("forgotten canonical plaintext resurrected: %q %v", section, err)
	}
}
