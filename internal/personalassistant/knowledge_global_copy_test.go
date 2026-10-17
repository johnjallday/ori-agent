package personalassistant

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

// workspaceFolders resolves the fixture HQ to its folder and every other
// workspace ID to a folder of its own, so copies land outside the HQ.
type workspaceFolders struct{ hq, root string }

func (f workspaceFolders) GetFolderPath(id string) (string, error) {
	if id == "hq-local" {
		return f.hq, nil
	}
	path := filepath.Join(f.root, id)
	return path, os.MkdirAll(path, 0o750)
}

func newGlobalCopyService(t *testing.T) (*KnowledgeLearningService, *workspace.MemoryStore, workspaceFolders) {
	t.Helper()
	f, _, _ := newWorkspaceProposalService(t)
	folders := workspaceFolders{hq: f.folder.path, root: t.TempDir()}
	memory := workspace.NewMemoryStore(folders)
	svc := NewKnowledgeLifecycleService(NewKnowledgeStore(f.resolver(), f.folder), memory, approvingTestAuthority{})
	return svc, memory, folders
}

func TestCopyGlobalFactAppendsOneOrdinaryEntryWithProvenance(t *testing.T) {
	svc, memory, folders := newGlobalCopyService(t)
	ctx := context.Background()
	_, fact, err := svc.PromoteWorkspaceEntry(ctx, "local", promoteRequest("John delivers stems at 48 kHz."))
	if err != nil {
		t.Fatal(err)
	}
	created, err := svc.CopyGlobalFact(ctx, "local", fact.ID, fact.Version, "ws-ep", "2026-10-17")
	if err != nil || !created {
		t.Fatalf("copy = %v %v", created, err)
	}
	doc, err := memory.Read("ws-ep")
	if err != nil {
		t.Fatal(err)
	}
	entries := doc.Entries()
	if len(entries) != 1 || entries[0].Type != workspace.MemoryTypeFact || entries[0].Date != "2026-10-17" ||
		entries[0].Provenance != "global:"+fact.ID || entries[0].Text != "John delivers stems at 48 kHz." {
		t.Fatalf("copied entry = %+v", entries)
	}
	if created, err := svc.CopyGlobalFact(ctx, "local", fact.ID, fact.Version, "ws-ep", "2026-10-17"); err != nil || created {
		t.Fatalf("repeat copy = %v %v", created, err)
	}
	holders, err := svc.GlobalFactCopies(ctx, "local", fact.ID, []string{"ws-album", "ws-ep", "hq-local", "ws-empty"})
	if err != nil || len(holders) != 1 || holders[0] != "ws-ep" {
		t.Fatalf("copies = %v %v", holders, err)
	}
	if _, err := os.Stat(filepath.Join(folders.root, "ws-empty", workspace.MemoryFileName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the copies scan created MEMORY.md: %v", err)
	}
}

func TestCopyGlobalFactRefusesTheHQStaleVersionsAndUnapprovedFacts(t *testing.T) {
	svc, _, _ := newGlobalCopyService(t)
	ctx := context.Background()
	_, fact, err := svc.PromoteWorkspaceEntry(ctx, "local", promoteRequest("John delivers stems at 48 kHz."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CopyGlobalFact(ctx, "local", fact.ID, fact.Version, "hq-local", "2026-10-17"); !errors.Is(err, ErrValidation) {
		t.Fatalf("copy into HQ = %v", err)
	}
	if _, err := svc.CopyGlobalFact(ctx, "local", fact.ID, fact.Version+1, "ws-ep", "2026-10-17"); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale version = %v", err)
	}
	if _, err := svc.CopyGlobalFact(ctx, "local", "missing", 1, "ws-ep", "2026-10-17"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown fact = %v", err)
	}
	_, candidate, err := svc.ProposeWorkspaceFact(ctx, "local", workspaceFact("ws-album", "John mixes at night."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CopyGlobalFact(ctx, "local", candidate.ID, candidate.Version, "ws-ep", "2026-10-17"); !errors.Is(err, ErrConflict) {
		t.Fatalf("pending candidate copied = %v", err)
	}
	if _, err := svc.CopyGlobalFact(ctx, "someone-else", fact.ID, fact.Version, "ws-ep", "2026-10-17"); !errors.Is(err, ErrGlobalMemoryUnavailable) {
		t.Fatalf("no assistant = %v", err)
	}
}

func TestCopiesOutliveForgetAndRemoveCopyDeletesOnlyThatLine(t *testing.T) {
	svc, memory, _ := newGlobalCopyService(t)
	ctx := context.Background()
	_, fact, err := svc.PromoteWorkspaceEntry(ctx, "local", promoteRequest("John delivers stems at 48 kHz."))
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"ws-ep", "ws-single"} {
		if _, err := svc.CopyGlobalFact(ctx, "local", fact.ID, fact.Version, target, "2026-10-17"); err != nil {
			t.Fatal(err)
		}
	}
	if err := memory.Append("ws-ep", workspace.MemoryEntry{Type: workspace.MemoryTypeFact, Date: "2026-10-17", Provenance: "user", Text: "EP keeps this"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ForgetItem(ctx, "local", fact.ID, fact.Version, "forget-copied"); err != nil {
		t.Fatal(err)
	}
	// Forget does not touch the copies, and they can still be found.
	holders, err := svc.GlobalFactCopies(ctx, "local", fact.ID, []string{"ws-ep", "ws-single"})
	if err != nil || strings.Join(holders, ",") != "ws-ep,ws-single" {
		t.Fatalf("copies after forget = %v %v", holders, err)
	}
	if err := svc.RemoveGlobalFactCopy(ctx, "local", fact.ID, "ws-ep"); err != nil {
		t.Fatal(err)
	}
	doc, err := memory.Read("ws-ep")
	if err != nil || len(doc.Entries()) != 1 || doc.Entries()[0].Text != "EP keeps this" {
		t.Fatalf("remove copy touched other lines: %+v %v", doc.Entries(), err)
	}
	if err := svc.RemoveGlobalFactCopy(ctx, "local", fact.ID, "ws-ep"); !errors.Is(err, workspace.ErrMemoryEntryNotFound) {
		t.Fatalf("second remove = %v", err)
	}
	if err := svc.RemoveGlobalFactCopy(ctx, "local", "missing", "ws-single"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown fact = %v", err)
	}
}
