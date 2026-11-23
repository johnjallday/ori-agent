//go:build darwin || linux || freebsd

package folderagent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/llm"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestRememberForDocumentsSharesCanonicalEntryAndForgetRetiresProof(t *testing.T) {
	root := build(t, "three.txt")
	h := newHarness(t, root)
	h.svc.deps.KnownFolderRoot = knownOnly("documents", root)
	id := h.start(t, "").WorkspaceID
	h.svc.Wait()
	rec, _ := h.svc.deps.Store.Read(id)
	h.svc.deps.DocumentsAccess = func(string, string) (DocumentsAccess, error) {
		return DocumentsAccess{root, rec.AgentName, "binding"}, nil
	}
	mem := workspace.NewMemoryStore(reviewMemoryFolder(t.TempDir()))
	h.svc.deps.OrganizationMemory = mem
	preview := func() DocumentsConvention {
		t.Helper()
		p, err := h.svc.OrganizationPreview("u1", id)
		if err != nil {
			t.Fatal(err)
		}
		return p.Convention
	}
	empty := preview()
	const rule = "File invoices in the Invoices folder under each property."
	saved, err := h.svc.SaveConvention("u1", id, empty.Revision, rule)
	if err != nil || saved.Text != rule || saved.Proof == "" {
		t.Fatal(saved, err)
	}
	if _, err = h.svc.SaveConvention("u1", id, empty.Revision, "Stale replacement"); err == nil {
		t.Fatal("stale edit accepted")
	}
	if err := os.WriteFile(filepath.Join(root, "three.txt"), []byte("Invoice for Pine Street."), 0600); err != nil {
		t.Fatal(err)
	}
	h.svc.deps.ReviewModel = func(string, string) (ReviewProcessor, ReviewChat) {
		return ReviewProcessor{"Cloud", "model", "pin"}, reviewChatFunc(func(context.Context, llm.ChatRequest) (*llm.ChatResponse, error) {
			return &llm.ChatResponse{Content: `{"groups":[],"placements":[{"ref":"s1","folder":"Pine Street/Invoices"}]}`, FinishReason: "end_turn"}, nil
		})
	}
	batch, err := h.svc.OrganizationPreview("u1", id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.svc.ApproveOrganization(t.Context(), "u1", id, batch.Approval, ReviewSelection{}); err != nil {
		t.Fatal(err)
	}
	placement := func() *PlacementSuggestion {
		t.Helper()
		history, err := h.svc.OrganizationResults("u1", id)
		if err != nil || len(history) != 1 || len(history[0].Files) != 1 {
			t.Fatalf("history %+v: %v", history, err)
		}
		return history[0].Files[0].Placement
	}
	if p := placement(); p == nil || !p.Convention || p.Destination != "Pine Street/Invoices" {
		t.Fatal("taught rule not applied")
	}
	snapshot, _ := mem.SnapshotExact(id)
	e := snapshot.Entries[0]
	e.Entry.Text = "Leave invoices loose."
	if err := mem.EditExact(id, e.Target, &e.Entry); err != nil {
		t.Fatal(err)
	}
	changed := preview()
	if changed.Text != e.Entry.Text || placement() != nil {
		t.Fatal("edited rule retained obsolete suggestion")
	}
	forgot, err := h.svc.SaveConvention("u1", id, changed.Revision, "")
	if err != nil || forgot.Text != "" {
		t.Fatal(forgot, err)
	}
	newRule, err := h.svc.SaveConvention("u1", id, forgot.Revision, rule)
	if err != nil || newRule.Proof == saved.Proof || placement() != nil {
		t.Fatal("a newly stated rule revived a forgotten proposal", err)
	}
}
