//go:build unix

package folderagent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestDocumentsVaultChoiceIsScopedAndRevalidated(t *testing.T) {
	root := build(t, "Alpha/.obsidian/", "Alpha/Test.md", "Work/Beta/.obsidian/", "Work/Beta/Test.md")
	h, id := guidedLook(t, root)
	ctx := t.Context()
	scope := DocumentsScope{Setup: true}
	h.svc.deps.DocumentsScope = func(context.Context, string) (DocumentsScope, error) { return scope, nil }
	offer, err := h.svc.DocumentsOffer(ctx, "u1", "hq", "attempt", id)
	if err != nil || offer == nil || len(offer.Vaults) != 2 || len(offer.Projects) != 0 {
		t.Fatalf("vault-only: %+v %v", offer, err)
	}
	in := DocumentsDraftInput{HQ: "hq", Attempt: "attempt", Report: offer.Report, Candidate: offer.Vaults[1].Ref}
	token, err := h.svc.ChooseDocumentsVault(ctx, "u1", id, in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.ResolveDocumentsVault(ctx, "u2", token); err == nil {
		t.Fatal("foreign token")
	}
	scope.Notebook = t.TempDir()
	if _, err := h.svc.ResolveDocumentsVault(ctx, "u1", token); err == nil {
		t.Fatal("concurrent connection ignored")
	}
	scope.Notebook = ""
	p, err := h.svc.ResolveDocumentsVault(ctx, "u1", token)
	if err != nil || filepath.Base(p) != "Beta" {
		t.Fatal(p, err)
	}
	if err := os.Rename(p, p+"-old"); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(p, ".obsidian"), 0750); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.ResolveDocumentsVault(ctx, "u1", token); err == nil {
		t.Fatal("replacement vault accepted")
	}
}
