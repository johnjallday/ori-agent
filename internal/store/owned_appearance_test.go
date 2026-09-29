package store

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/johnjallday/ori-agent/internal/config"
	"github.com/johnjallday/ori-agent/internal/workspacecontinuity"
)

func TestOwnedAppearanceReaderExcludesRosterFallbackAndUnsafeFiles(t *testing.T) {
	sandbox := t.TempDir()
	t.Setenv("HOME", filepath.Join(sandbox, "home"))
	t.Setenv("ORI_DATA_DIR", filepath.Join(sandbox, "data"))
	c, _, source := compositeWithWorkspaces(t, workspaceCopy("other", "Other", "Stranger", "foreign"))
	if _, exists := c.GetOwnedAgent("Stranger"); exists || source.calls != 0 {
		t.Fatal("owned definition reader consulted the workspace roster")
	}
	if _, err := c.ReadOwnedAgentAppearance(t.Context(), "Stranger", "face.png"); !errors.Is(err, ErrWorkspaceOwnedAgent) || source.calls != 0 {
		t.Fatal("workspace roster authorized global image lookup", err)
	}
	if err := c.CreateAgent("Guide", nil); err != nil {
		t.Fatal(err)
	}
	ag, _ := c.GetOwnedAgent("Guide")
	ag.EnsureAppearance()
	ag.Appearance.SetUpload("face.png")
	if err := c.SetAgent("Guide", ag); err != nil {
		t.Fatal(err)
	}
	folder, ok := c.RootAgentFolder("Guide")
	if !ok {
		t.Fatal("missing root owner")
	}
	image := []byte("owned image bytes; destination validates image semantics")
	if err := os.WriteFile(filepath.Join(folder, "face.png"), image, 0600); err != nil {
		t.Fatal(err)
	}
	got, err := c.ReadOwnedAgentAppearance(t.Context(), "Guide", "face.png")
	if err != nil || !bytes.Equal(got, image) {
		t.Fatal("owned read failed", err)
	}
	if _, err := c.ReadOwnedAgentAppearance(t.Context(), "Guide", "other.png"); !errors.Is(err, ErrWorkspaceOwnedAgent) {
		t.Fatal("appearance did not authorize this filename", err)
	}
	if err := os.Remove(filepath.Join(folder, "face.png")); err != nil {
		t.Fatal(err)
	}
	shared := config.DefaultAgentAvatarsDir()
	if err := os.MkdirAll(shared, 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(shared, "face.png"), image, 0600); err != nil {
		t.Fatal(err)
	}
	if got, err := c.ReadOwnedAgentAppearance(t.Context(), "Guide", "face.png"); err != nil || !bytes.Equal(got, image) {
		t.Fatal("owned legacy image did not migrate", err)
	}
	// A link in the more specific location is unsafe, not permission to fall
	// back to the valid shared file. No bytes outside that root are read.
	if err := os.Symlink(filepath.Join(shared, "face.png"), filepath.Join(folder, "face.png")); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ReadOwnedAgentAppearance(t.Context(), "Guide", "face.png"); !errors.Is(err, workspacecontinuity.ErrUnsafe) {
		t.Fatal("linked source triggered fallback", err)
	}
	if err := os.Remove(filepath.Join(folder, "face.png")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, definitionFileName), []byte(`{"appearance":null}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ReadOwnedAgentAppearance(t.Context(), "Guide", "face.png"); !errors.Is(err, ErrAgentChangedOnDisk) {
		t.Fatal("stale global definition authorized image bytes", err)
	}
}
