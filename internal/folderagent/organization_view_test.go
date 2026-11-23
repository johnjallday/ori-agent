//go:build darwin || linux || freebsd

package folderagent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/llm"
)

func TestSavedReviewKeepsSourcesAndDecisionsWithoutAdditionalCalls(t *testing.T) {
	root := build(t, "packing.txt", "ref.txt")
	for _, name := range []string{"packing.txt", "ref.txt"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("Atrium remodel · AB-1931"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	h := newHarness(t, root)
	h.svc.deps.KnownFolderRoot = knownOnly("documents", root)
	id := h.start(t, "").WorkspaceID
	h.svc.Wait()
	rec, _ := h.svc.deps.Store.Read(id)
	h.svc.deps.DocumentsAccess = func(string, string) (DocumentsAccess, error) {
		return DocumentsAccess{root, rec.AgentName, "grant"}, nil
	}
	calls := 0
	h.svc.deps.ReviewModel = func(string, string) (ReviewProcessor, ReviewChat) {
		return ReviewProcessor{"Cloud", "model", "test"}, reviewChatFunc(func(context.Context, llm.ChatRequest) (*llm.ChatResponse, error) {
			calls++
			return &llm.ChatResponse{Content: `{"groups":[{"title":"Atrium remodel","members":["s1","s2"],"links":[{"a":"s1","b":"s2","key":"AB-1931"}]}]}`, FinishReason: "end_turn"}, nil
		})
	}
	p, _ := h.svc.OrganizationPreview("u1", id)
	v, err := h.svc.ApproveOrganization(t.Context(), "u1", id, p.Approval, ReviewSelection{})
	if err != nil || len(v.Groups) != 1 {
		t.Fatal(v, err)
	}
	g := v.Groups[0]
	keep := ViewChange{Review: v.ID, Group: g.ID, Action: "keep"}
	for range 2 {
		v, err = h.svc.ChangeOrganizationView("u1", id, keep)
		if err != nil || !v.Groups[0].Kept || len(v.Decisions) != 1 {
			t.Fatal(v, err)
		}
	}
	rename := ViewChange{Review: v.ID, Group: g.ID, Action: "rename", Name: "Home renovation"}
	if _, err := h.svc.ChangeOrganizationView("u1", id, rename); err == nil {
		t.Fatal("stale edit accepted")
	}
	rename.Revision = v.Revision
	v, err = h.svc.ChangeOrganizationView("u1", id, rename)
	if err != nil || v.Groups[0].Title != rename.Name || len(v.Decisions) != 2 {
		t.Fatal(v, err)
	}
	p, err = h.svc.OrganizationPreview("u1", id)
	if err != nil || len(p.Files) != 0 {
		t.Fatal("already reviewed files in next batch")
	}
	opened := ""
	h.svc.deps.ReviewOpen = func(path string) error { opened = path; return nil }
	src := v.Files[0].Source
	if err := h.svc.InspectOrganizationSource(t.Context(), "u1", id, v.ID, src.Ref); err != nil || opened != filepath.Join(root, src.Path) {
		t.Fatal("wrong viewer dispatch", err)
	}
	if err := os.Rename(opened, opened+".old"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(opened, []byte("new"), 0600); err != nil {
		t.Fatal(err)
	}
	opened = ""
	if err := h.svc.InspectOrganizationSource(t.Context(), "u1", id, v.ID, src.Ref); err == nil || opened != "" {
		t.Fatal("replaced file opened")
	}
	if calls != 1 {
		t.Fatal("view operations resent content")
	}
}
