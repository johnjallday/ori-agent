//go:build darwin || linux || freebsd

package folderagent

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/filejanitor"
)

func TestCheckOrganizationFilingRequiresCurrentSavedMembership(t *testing.T) {
	root := build(t, "one.pdf", "other.pdf")
	h := newHarness(t, root)
	h.svc.deps.KnownFolderRoot = knownOnly("documents", root)
	id := h.start(t, "").WorkspaceID
	h.svc.Wait()
	r, _ := h.svc.deps.Store.Read(id)
	h.svc.deps.DocumentsAccess = func(string, string) (DocumentsAccess, error) {
		return DocumentsAccess{root, r.AgentName, "binding"}, nil
	}
	_, auth, err := h.svc.latestReview("u1", id)
	if err != nil {
		t.Fatal(err)
	}
	_, err = h.svc.deps.Store.Update(id, func(r *Record) error {
		r.OrganizationRuns = []OrganizationRun{{Authority: auth, Result: ReviewResult{ID: "review", Status: "reviewed", Revision: 3, Files: []ReviewedDocument{{Source: ReviewSource{Path: "one.pdf", Ref: "ok"}, Status: "read"}, {Source: ReviewSource{Path: "other.pdf", Ref: "nonmember"}, Status: "read"}, {Source: ReviewSource{Path: "Examples/ref.pdf", Ref: "example", Example: true}, Status: "read"}}, Groups: []DocumentGroup{{ID: "group", Kept: true, Members: []string{"ok", "example"}}}}}}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	h.svc.deps.MatchFiling = func(_ context.Context, owner, ws, path, witness string, selected []filejanitor.ReviewedLooseFile) (filejanitor.DocumentsSelection, error) {
		calls++
		if owner != "u1" || ws != id || path != root || witness == "" || len(selected) != 1 || selected[0].Name != "one.pdf" {
			t.Fatalf("unexpected selection %+v", selected)
		}
		return filejanitor.DocumentsSelection{State: "scan"}, nil
	}
	req := FilingSelection{Review: "review", Revision: 3, Group: "group", Sources: []string{"ok"}}
	if got, err := h.svc.CheckOrganizationFiling(t.Context(), "u1", id, req); err != nil || got.State != "scan" {
		t.Fatal(got, err)
	}
	for _, refs := range [][]string{{"ok", "ok"}, {"nonmember"}, {"example"}, {"one.pdf"}, nil} {
		bad := req
		bad.Sources = refs
		if _, err := h.svc.CheckOrganizationFiling(t.Context(), "u1", id, bad); err == nil {
			t.Fatalf("accepted %v", refs)
		}
	}
	stale := req
	stale.Revision--
	if _, err := h.svc.CheckOrganizationFiling(t.Context(), "u1", id, stale); err == nil {
		t.Fatal("stale view")
	}
	h.svc.deps.DocumentsProtected = func() []string { return []string{filepath.Join(root, "one.pdf")} }
	if _, err := h.svc.CheckOrganizationFiling(t.Context(), "u1", id, req); err == nil {
		t.Fatal("protected source")
	}
	if calls != 1 {
		t.Fatal("ineligible selection reached Filing")
	}
}
