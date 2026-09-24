package personalassistant

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestGenericHQMemoryToolAppendsUnderLifecycleLockAndFailsClosedWithoutIt(t *testing.T) {
	ctx := context.Background()
	f := newKnowledgeFixture(t)
	s := approvedTestService(f)
	candidate, _, err := s.Propose(ctx, "local", testProposal("app-a", "A pending reviewed preference"))
	if err != nil {
		t.Fatal(err)
	}
	entry := workspace.MemoryEntry{Type: workspace.MemoryTypeFact, Date: "2026-09-01", Provenance: "agent:Atlas", Text: "A pending reviewed preference"}
	if err := s.AppendGenericMemoryWrite(ctx, "local", "hq-local", entry); !errors.Is(err, workspace.ErrMemoryManaged) {
		t.Fatalf("candidate bypassed review through atomic tool append: %v", err)
	}
	before, err := s.store.Read(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	entry.Text = "Unrelated operational baseline"
	if err := s.AppendGenericMemoryWrite(ctx, "local", "hq-local", entry); err != nil {
		t.Fatalf("ordinary operational memory was blocked: %v", err)
	}
	after, err := s.store.Read(ctx, "local")
	if err != nil || after.Version != before.Version {
		t.Fatalf("ordinary tool append mutated review metadata or quota: %d -> %d, %v", before.Version, after.Version, err)
	}
	if _, err := s.SuppressCandidate(ctx, "local", candidate.ID, candidate.Version, KnowledgeRejected); err != nil {
		t.Fatal(err)
	}
	restarted := approvedTestService(f)
	entry.Text = "A pending reviewed preference"
	if err := restarted.AppendGenericMemoryWrite(ctx, "local", "hq-local", entry); !errors.Is(err, workspace.ErrMemoryManaged) {
		t.Fatalf("rejected fact restored after restart: %v", err)
	}
	doc, err := f.memory.Read("hq-local")
	if err != nil || len(doc.Entries()) != 1 || doc.Entries()[0].Text != "Unrelated operational baseline" {
		t.Fatalf("guarded write changed canonical memory: %+v %v", doc.Entries(), err)
	}
	if err := os.Remove(filepath.Join(f.folder.path, workspace.SidecarDirName, knowledgeLockName)); err != nil {
		t.Fatal(err)
	}
	entry.Text = "Another ordinary fact"
	if err := restarted.AppendGenericMemoryWrite(ctx, "local", "hq-local", entry); !errors.Is(err, ErrRepairNeeded) {
		t.Fatalf("missing lock allowed unsynchronized append: %v", err)
	}
	if err := restarted.AppendGenericMemoryWrite(ctx, "local", "foreign", entry); !errors.Is(err, ErrRepairNeeded) {
		t.Fatalf("foreign workspace used bound tool: %v", err)
	}
}
