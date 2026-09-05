package samplelibrary

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAnalysisConsentDoesNotReadUntilRefreshAndRevokeDeletesDerivedFacts(t *testing.T) {
	ctx := context.Background()
	service, store, _, selections, homeID := newTestService(t)
	rootPath := t.TempDir()
	if err := os.WriteFile(filepath.Join(rootPath, "kick.wav"), []byte("audio bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	token, _ := selections.Issue(rootPath)
	connectReview, err := service.ReviewRoot(ctx, homeID, token)
	if err != nil {
		t.Fatal(err)
	}
	state, root, err := service.CommitRoot(ctx, homeID, connectReview.Token, token, "connect")
	if err != nil {
		t.Fatal(err)
	}
	analysisReview, err := service.ReviewAnalysis(ctx, homeID, root.ID, true, true)
	if err != nil {
		t.Fatal(err)
	}
	state, root, err = service.CommitAnalysis(ctx, homeID, root.ID, analysisReview.Token, "enable", true, true)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := store.Entries(ctx, homeID, root.ID, 200)
	if err != nil || len(entries) != 0 {
		t.Fatalf("consent unexpectedly scanned: %#v %v", entries, err)
	}
	result, err := service.Index(ctx, homeID, root.ID, "refresh", state.CatalogRevision, root.Revision)
	if err != nil {
		t.Fatal(err)
	}
	entries, err = store.Entries(ctx, homeID, root.ID, 200)
	if err != nil || len(entries) != 1 || len(entries[0].SHA256) != 64 {
		t.Fatalf("hash after refresh=%#v err=%v", entries, err)
	}
	revoke, err := service.ReviewAnalysis(ctx, homeID, root.ID, false, false)
	if err != nil {
		t.Fatal(err)
	}
	_, root, err = service.CommitAnalysis(ctx, homeID, root.ID, revoke.Token, "revoke", false, false)
	if err != nil {
		t.Fatal(err)
	}
	if root.HashEnabled || root.TagsEnabled {
		t.Fatalf("analysis remained enabled: %+v", root)
	}
	entries, err = store.Entries(ctx, homeID, root.ID, 200)
	if err != nil || entries[0].SHA256 != "" {
		t.Fatalf("revoked hash retained: %#v %v", entries, err)
	}
	revokeRoot, err := service.ReviewRevocation(ctx, homeID, root.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, root, err = service.CommitRevocation(ctx, homeID, root.ID, revokeRoot.Token, "revoke-root")
	if err != nil {
		t.Fatal(err)
	}
	if root.State != "revoked" {
		t.Fatalf("root state=%s", root.State)
	}
	if _, err = os.Stat(filepath.Join(rootPath, "kick.wav")); err != nil {
		t.Fatalf("revocation changed source: %v", err)
	}
	if _, err = service.Entries(ctx, homeID, root.ID, 200); !errors.Is(err, ErrRootMissing) {
		t.Fatalf("revoked root remained searchable: %v", err)
	}
	redacted, err := store.Entries(ctx, homeID, root.ID, 200)
	if err != nil || len(redacted) != 1 || redacted[0].Filename != "Unavailable" || strings.Contains(redacted[0].RelativeLocator, "kick") {
		t.Fatalf("tombstone was not redacted: %#v %v", redacted, err)
	}
	_ = result
}

func TestCapabilityRuntimeRemovalRedactsCatalogAndPreservesSource(t *testing.T) {
	ctx := context.Background()
	service, store, workspaces, selections, homeID := newTestService(t)
	rootPath := t.TempDir()
	source := filepath.Join(rootPath, "snare.wav")
	if err := os.WriteFile(source, []byte("source"), 0600); err != nil {
		t.Fatal(err)
	}
	token, _ := selections.Issue(rootPath)
	review, err := service.ReviewRoot(ctx, homeID, token)
	if err != nil {
		t.Fatal(err)
	}
	state, root, err := service.CommitRoot(ctx, homeID, review.Token, token, "connect")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Index(ctx, homeID, root.ID, "index", state.CatalogRevision, root.Revision); err != nil {
		t.Fatal(err)
	}
	runtime := NewCapabilityRuntime(service)
	if err = runtime.OnCapabilityRemove(homeID); err != nil {
		t.Fatal(err)
	}
	if data, readErr := os.ReadFile(source); readErr != nil || string(data) != "source" {
		t.Fatalf("source changed: %q %v", data, readErr)
	}
	ws, err := workspaces.Get(homeID)
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range ws.DirectoryReferences {
		if ref.Purpose == "sample_library" {
			t.Fatalf("reference retained: %+v", ref)
		}
	}
	saved, err := store.Get(ctx, homeID)
	if err != nil || saved.Lifecycle != "disabled" {
		t.Fatalf("state=%+v %v", saved, err)
	}
	redacted, err := store.Entries(ctx, homeID, root.ID, 200)
	if err != nil || redacted[0].Filename != "Unavailable" {
		t.Fatalf("catalog not redacted: %#v %v", redacted, err)
	}
}
