package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/personalassistant"
	"github.com/johnjallday/ori-agent/internal/projecttemplates"
	"github.com/johnjallday/ori-agent/internal/workspace"
)

// fakeFolderScanDigest is the slice of the offer service the authority
// reads: which resolved project offer a folder key belongs to.
type fakeFolderScanDigest struct {
	offers map[string]personalassistant.FolderOffer
}

func (f fakeFolderScanDigest) ResolvedProjectOfferForKey(_ context.Context, _ string, key string) (personalassistant.FolderOffer, bool, error) {
	offer, ok := f.offers[key]
	return offer, ok, nil
}

func folderScanItem(key string) personalassistant.KnowledgeItem {
	return personalassistant.KnowledgeItem{
		ID: "item-1", SourceKind: personalassistant.FolderScanSourceKind, Category: "projects",
		CurrentRevisionID: "rev-1",
		Revisions: []personalassistant.KnowledgeRevision{{
			ID:       "rev-1",
			Evidence: []personalassistant.KnowledgeEvidence{{SourceKind: personalassistant.FolderScanSourceKind, SourceID: key}},
		}},
	}
}

func TestFolderScanAuthority_RevalidatesThroughTheLinkedWorkspace(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	thesis := filepath.Join(root, "Thesis")
	if err := os.MkdirAll(thesis, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(thesis, "main.tex"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	files, err := workspace.NewFileStore(filepath.Join(root, "workspaces"))
	if err != nil {
		t.Fatal(err)
	}
	ws := &workspace.Workspace{ID: "ws-thesis", Name: "Thesis", Status: workspace.StatusActive}
	if _, err := projecttemplates.AttachLinkedDirectory(ws, "Thesis", thesis); err != nil {
		t.Fatal(err)
	}
	if err := files.Save(ws); err != nil {
		t.Fatal(err)
	}
	key := personalassistant.FolderKey(thesis)
	offer := personalassistant.FolderOffer{
		ID: "offer-1", Status: personalassistant.FolderOfferResolved,
		Subject: personalassistant.FolderCandidateRecord{Key: key, Name: "Thesis", Kind: "project", MarkerName: "main.tex"},
		Outcome: &personalassistant.FolderOutcome{Kind: "project", WorkspaceID: "ws-thesis"},
	}
	authority := folderScanAuthority{
		digest: fakeFolderScanDigest{offers: map[string]personalassistant.FolderOffer{key: offer}},
		files:  files,
		validate: func(raw string) (string, error) {
			return filepath.EvalSymlinks(raw)
		},
	}
	binding := personalassistant.KnowledgeBinding{UserID: "local"}

	if err := authority.Revalidate(context.Background(), binding, folderScanItem(key)); err != nil {
		t.Fatalf("live folder: %v", err)
	}
	// The marker leaves: the fact needs review.
	if err := os.Remove(filepath.Join(thesis, "main.tex")); err != nil {
		t.Fatal(err)
	}
	if err := authority.Revalidate(context.Background(), binding, folderScanItem(key)); err == nil {
		t.Fatal("missing marker still revalidated")
	}
	// A tool candidate without a marker requirement still needs the folder.
	offer.Subject.MarkerName = ""
	authority.digest = fakeFolderScanDigest{offers: map[string]personalassistant.FolderOffer{key: offer}}
	if err := authority.Revalidate(context.Background(), binding, folderScanItem(key)); err != nil {
		t.Fatalf("folder without marker: %v", err)
	}
	if err := os.RemoveAll(thesis); err != nil {
		t.Fatal(err)
	}
	if err := authority.Revalidate(context.Background(), binding, folderScanItem(key)); err == nil {
		t.Fatal("deleted folder still revalidated")
	}
	// Unknown keys, foreign source kinds, and malformed evidence never pass.
	if err := authority.Revalidate(context.Background(), binding, folderScanItem("0000")); err == nil {
		t.Fatal("unknown key revalidated")
	}
	other := folderScanItem(key)
	other.SourceKind = "saved_app"
	if err := authority.Revalidate(context.Background(), binding, other); err == nil {
		t.Fatal("foreign source kind revalidated")
	}
}
