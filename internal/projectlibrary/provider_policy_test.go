package projectlibrary

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/workspace"
)

func TestProviderEvidence_DisablePreservesHistoryButBlocksEveryNewGrant(t *testing.T) {
	file, scope := libraryHome(t)
	available := true
	store := NewStore(file).WithProviderEvidence(func(candidate Scope, home *workspace.Workspace) bool {
		return available && candidate == scope && home.ID == scope.HomeID
	})
	init, err := store.ReviewInitialize(scope)
	if err != nil {
		t.Fatal(err)
	}
	available = false
	if _, _, err := store.CommitInitialize(scope, init.Token, "provider-init"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("provider removed after initialization review: %v", err)
	}
	if _, err := store.ReviewInitialize(scope); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("provider removed before initialization review: %v", err)
	}
	available = true
	if _, _, err := store.CommitInitialize(scope, init.Token, "provider-init"); err != nil {
		t.Fatal(err)
	}
	r, _, _, picker := rootTestService(t)
	r.library = store
	picker.path, _ = filepath.EvalSymlinks(newMusicTree(t).root)
	picked, err := r.Pick(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := store.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	review, err := r.Review(scope, picked, doc.Revision)
	if err != nil {
		t.Fatal(err)
	}
	available = false
	if _, _, err := r.Commit(scope, review.Token, "provider-root"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("provider removed after root review: %v", err)
	}
	if _, err := r.Pick(context.Background(), scope); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("provider removed before native picker: %v", err)
	}
	if _, err := store.Read(scope); err != nil {
		t.Fatalf("provider removal hid historical metadata: %v", err)
	}
	available = true
	root, _, err := r.Commit(scope, review.Token, "provider-root")
	if err != nil {
		t.Fatal(err)
	}
	available = false
	if _, err := r.VerifyConnectedRoot(scope, root.ID); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("provider removal still permitted reading source: %v", err)
	}
	doc, err = store.Read(scope)
	if err != nil {
		t.Fatal(err)
	}
	revoke, err := r.ReviewRevoke(scope, root.ID, doc.Revision)
	if err != nil {
		t.Fatalf("owner could not revoke after provider removal: %v", err)
	}
	if _, _, err := r.CommitRevoke(scope, root.ID, revoke.Token, "provider-revoke"); err != nil {
		t.Fatalf("owner could not finish revocation after provider removal: %v", err)
	}
	available = true
	if _, err := r.VerifyConnectedRoot(scope, root.ID); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("re-enabling provider revived revoked root: %v", err)
	}
}
