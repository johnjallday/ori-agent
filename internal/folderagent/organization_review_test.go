//go:build darwin || linux || freebsd

package folderagent

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestOrganizationPreviewRequiresLiveDocumentsAndOriginalLook(t *testing.T) {
	root := build(t, "estimate.pdf", "cabinet.docx", "scan.pdf", "examples/reference.pdf", "other.png")
	h := newHarness(t, root)
	h.svc.deps.KnownFolderRoot = knownOnly("documents", root)
	id := h.start(t, "").WorkspaceID
	h.svc.Wait()
	// This is an older normal hire, without the newer HQ walk binding. The
	// display name is not part of the Documents recognition policy.
	_, err := h.svc.deps.Store.Update(id, func(r *Record) error { r.AgentName = "Maple"; r.State = StateKept; return nil })
	if err != nil {
		t.Fatal(err)
	}
	a := DocumentsAccess{Root: root, Lead: "Maple", Binding: "binding"}
	h.svc.deps.DocumentsAccess = func(owner, ws string) (DocumentsAccess, error) {
		if owner != "u1" || ws != id {
			return DocumentsAccess{}, ErrNotOwner
		}
		return a, nil
	}
	p, err := h.svc.OrganizationPreview("u1", id)
	if err != nil || !slices.Equal(p.Files, []string{"cabinet.docx", "estimate.pdf", "scan.pdf"}) {
		t.Fatalf("preview %+v: %v", p, err)
	}
	v, err := h.svc.View(t.Context(), "u1", id)
	if err != nil || !v.OrganizationReview {
		t.Fatal("no kept offer", err)
	}
	refused := func(owner string) {
		t.Helper()
		if _, err := h.svc.OrganizationPreview(owner, id); err == nil {
			t.Fatal("unexpected access")
		}
	}
	refused("other")
	a.Lead = "Replacement"
	refused("u1")
	a.Lead = "Maple"
	a.Binding = ""
	refused("u1")
	a.Binding = "grant"
	ordinary := filepath.Join(t.TempDir(), "Documents")
	if err := os.Mkdir(ordinary, 0750); err != nil {
		t.Fatal(err)
	}
	a.Root = ordinary
	refused("u1")
	a.Root = root
	if err := os.Rename(root, root+"-old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(root, 0750); err != nil {
		t.Fatal(err)
	}
	refused("u1")
}
