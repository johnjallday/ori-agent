//go:build darwin || linux || freebsd

package folderagent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/johnjallday/ori-agent/internal/foldermap"
)

func TestGuidedFilingRecommendationUsesOnlyObservedLooseDocuments(t *testing.T) {
	for _, tc := range []struct {
		name  string
		paths []string
		want  string
	}{{"eligible", []string{"memo.md"}, "eligible"}, {"empty", nil, "empty"}, {"unsupported", []string{"movie.mp4", "project/notes.md"}, "empty"}, {"protected", []string{"private.txt"}, "empty"}, {"hidden", []string{".secret.txt"}, "empty"}} {
		t.Run(tc.name, func(t *testing.T) {
			root := build(t, tc.paths...)
			h := newHarness(t, root)
			h.ws.anchors["saved"] = root
			h.svc.deps.KnownFolderRoot = knownOnly("documents", root)
			h.svc.deps.DocumentsProtected = func() []string { return []string{filepath.Join(root, "private.txt")} }
			h.svc.deps.AssignedRoot = func(context.Context, string, string, string, string) (string, string, bool) {
				return root, "Shelves", true
			}
			access := DocumentsAccess{Root: root, Lead: "Mira", Binding: "scoped"}
			h.svc.deps.DocumentsAccess = func(string, string) (DocumentsAccess, error) { return access, nil }
			if err := h.svc.StartAssigned(t.Context(), "local", "saved", "Mira", GuidedDocumentsLinkPrefix+"ref", "Review names"); err != nil {
				t.Fatal(err)
			}
			h.svc.Wait()
			check := func(want string) {
				t.Helper()
				v, err := h.svc.View(t.Context(), "local", "saved")
				if err != nil || v.FilingReviewHint != want || v.OrganizationReview != (want == "eligible" || want == "history") {
					t.Fatalf("want %s got %q/%v: %v", want, v.FilingReviewHint, v.OrganizationReview, err)
				}
			}
			check(tc.want)
			if tc.name != "eligible" {
				return
			}
			preview, err := h.svc.OrganizationPreview("local", "saved")
			if err != nil || len(preview.Files) != 1 || preview.Approval != "" {
				t.Fatalf("preview without processor: %+v %v", preview, err)
			}
			access.Binding = ""
			check("unavailable")
			access.Binding = "scoped"
			stamp := time.Now().Add(-time.Hour)
			if err := os.Chtimes(root, stamp, stamp); err != nil {
				t.Fatal(err)
			}
			check("stale")
			if _, err := h.svc.deps.Store.Update("saved", func(r *Record) error { r.LooseDocuments = nil; return nil }); err != nil {
				t.Fatal(err)
			}
			check("partial")
			if _, err := h.svc.deps.Store.Update("saved", func(r *Record) error {
				f, _ := r.Map.Folder("")
				r.OrganizationRuns = []OrganizationRun{{Authority: documentsRef("local", "saved", root, f.BranchWitness, "Mira", "scoped"), Result: ReviewResult{Status: "reviewed"}}}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			check("history")
		})
	}
}

func TestGuidedFilingRecommendationLargeRootIsUnknown(t *testing.T) {
	s := NewService(Deps{})
	root := t.TempDir()
	s.deps.KnownFolderRoot = knownOnly("documents", root)
	s.deps.DocumentsAccess = func(string, string) (DocumentsAccess, error) {
		return DocumentsAccess{Root: root, Lead: "A", Binding: "scoped"}, nil
	}
	m, err := foldermap.Walk(root, foldermap.Options{})
	if err != nil {
		t.Fatal(err)
	}
	m.Folders[0].State = foldermap.StateLarge
	r := &Record{OwnerUserID: "local", AgentName: "A", Map: m, State: StateReported, LooseDocuments: &LooseDocumentObservation{Eligible: true, RootModified: time.Now()}}
	if got := s.looseDocumentHint(r); got != "partial" {
		t.Fatal(got)
	}
}
