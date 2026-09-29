package projectlibrary

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// The macOS chooser returns "POSIX path of (choose folder)", which ends a
// folder path with a slash. The first real native pick in a manual session
// was refused because the selection was compared in clean form; the fake
// picker in every automated run had always returned clean paths.
func TestRoots_PickAcceptsNativeChooserTrailingSlash(t *testing.T) {
	r, scope, _, picker := rootTestService(t)
	tree := newMusicTree(t)
	clean, err := filepath.EvalSymlinks(tree.root)
	if err != nil {
		t.Fatal(err)
	}
	picker.path = clean + string(filepath.Separator)
	token, err := r.Pick(context.Background(), scope)
	if err != nil || token == "" {
		t.Fatalf("native chooser path with a trailing slash was refused: %q %v", token, err)
	}
	review, err := r.Review(scope, token, 1)
	if err != nil || review.RootPath != clean {
		t.Fatalf("review of the cleaned selection: %+v %v", review, err)
	}
	root, _, err := r.Commit(scope, review.Token, "connect-trailing-slash")
	if err != nil || root.Path != clean {
		t.Fatalf("connected root path = %q err=%v, want %q", root.Path, err, clean)
	}
	// A symlinked selection is still refused: cleaning never resolves links.
	link := filepath.Join(t.TempDir(), "songs-link")
	if err := os.Symlink(clean, link); err != nil {
		t.Skip("symlinks unavailable here")
	}
	picker.path = link + string(filepath.Separator)
	if _, err := r.Pick(context.Background(), scope); err == nil {
		t.Fatal("symlinked chooser selection was accepted")
	}
}
